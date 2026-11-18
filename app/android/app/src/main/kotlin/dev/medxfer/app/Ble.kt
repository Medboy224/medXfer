package dev.medxfer.app

import android.annotation.SuppressLint
import android.bluetooth.BluetoothAdapter
import android.bluetooth.BluetoothDevice
import android.bluetooth.BluetoothGatt
import android.bluetooth.BluetoothGattCallback
import android.bluetooth.BluetoothGattCharacteristic
import android.bluetooth.BluetoothGattServer
import android.bluetooth.BluetoothGattServerCallback
import android.bluetooth.BluetoothGattService
import android.bluetooth.BluetoothManager
import android.bluetooth.BluetoothProfile
import android.bluetooth.le.AdvertiseCallback
import android.bluetooth.le.AdvertiseData
import android.bluetooth.le.AdvertiseSettings
import android.bluetooth.le.ScanCallback
import android.bluetooth.le.ScanFilter
import android.bluetooth.le.ScanResult
import android.bluetooth.le.ScanSettings
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.ParcelUuid
import android.os.SystemClock
import java.io.ByteArrayOutputStream
import java.util.UUID

// The path by BLE (bible §6.6, WP-D.3 of the J6d plan): this device advertises medXfer's service,
// its UUID alone (D2); answers the devices that connect to it (GATT server); finds the others,
// by a scan filtered on the service, which Android keeps running screen off (WP-D.0); and
// carries a message to one of them (GATT client: a write, then a read of the answer). The core
// makes and reads every message: this class only carries bytes. System APIs only (D5).
//
// A computer is never left to connect to this phone (J8): paired with it in Bluetooth, Android
// leaves the computer's requests unanswered 20 to 50 s (real tests of 17 November 2026). This
// phone connects to it and keeps the link while both look for devices ("held"), subscribed to
// its call characteristic: the computer calls by a notification, this phone reads its message,
// and writes the core's answer back, then reads once more to mark the end. Over that link the
// computer reaches this phone as "held:" and its address, the conn of an exchange it started.
//
// Everything runs on the main thread: the Bluetooth callbacks are posted to it.
@SuppressLint("MissingPermission") // asked by the app before start (BLE-12)
class Ble(context: Context, private val emit: (String, Map<String, Any?>) -> Unit) {
    private val app = context.applicationContext
    private val manager = app.getSystemService(BluetoothManager::class.java)
    private val wifi = app.getSystemService(Context.WIFI_SERVICE) as WifiManager
    private val main = Handler(Looper.getMainLooper())

    val supported: Boolean get() = manager?.adapter != null && app.packageManager.hasSystemFeature("android.hardware.bluetooth_le")
    val enabled: Boolean get() = manager?.adapter?.isEnabled == true

    /** The Wi-Fi bands this phone can create a network on and join one on (bit 0 2.4 GHz, bit 1 5 GHz). */
    fun bands(): Map<String, Int> {
        val five = if (wifi.is5GHzBandSupported) 2 else 0
        val on = wifi.isWifiEnabled
        // The group at 5 GHz needs Wi-Fi on; the local hotspot, at 2.4 GHz, does without. With
        // Wi-Fi off no network can be joined: said, so that the other joins this phone's instead
        // (the TECNO BF6, its Wi-Fi off once its own hotspot stopped, real test of 2 November 2026).
        return mapOf("create" to (1 or if (on) five else 0), "join" to if (on) (1 or five) else 0)
    }

    // The server side: the GATT server and the advertisement.
    private var server: BluetoothGattServer? = null
    private var advertising: AdvertiseCallback? = null
    private val writes = mutableMapOf<String, ByteArrayOutputStream>() // long writes being prepared
    private val replies = mutableMapOf<String, ByteArray>() // the answer each client reads next
    private val parked = mutableMapOf<String, MutableList<Pair<Int, Int>>>() // reads before the answer: (request, offset)
    private val clientsOf = mutableMapOf<String, BluetoothDevice>()

    // Bluetooth turned off takes the server, the advertisement and the scan with it: made again
    // at the next start.
    private val radio = object : BroadcastReceiver() {
        override fun onReceive(context: Context, intent: Intent) {
            if (intent.getIntExtra(BluetoothAdapter.EXTRA_STATE, BluetoothAdapter.STATE_ON) != BluetoothAdapter.STATE_TURNING_OFF) return
            server?.close()
            server = null
            advertising = null
            scanning = null
        }
    }

    init {
        val filter = IntentFilter(BluetoothAdapter.ACTION_STATE_CHANGED)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            app.registerReceiver(radio, filter, Context.RECEIVER_NOT_EXPORTED)
        } else {
            app.registerReceiver(radio, filter)
        }
    }

    // The scan.
    private var scanning: ScanCallback? = null
    private val seenAt = mutableMapOf<String, Long>()

    // The client side: one GATT connection per device reached.
    private val clients = mutableMapOf<String, Client>()

    /** Advertises and answers ([advertise]), scans ([scan]). done gets an error, or null. */
    fun start(advertise: Boolean, scan: Boolean, done: (String?) -> Unit) {
        val a = manager?.adapter ?: return done("unsupported")
        if (!a.isEnabled) return done("bluetooth_off")
        if (advertise && advertising == null) {
            val error = serve(a.bluetoothLeAdvertiser ?: return done("no_advertiser"))
            if (error != null) return done(error)
        }
        if (scan && scanning == null) {
            val scanner = a.bluetoothLeScanner ?: return done("no_scanner")
            val cb = object : ScanCallback() {
                override fun onScanResult(callbackType: Int, r: ScanResult) = main.post { seen(r) }.let { }
                override fun onScanFailed(errorCode: Int) = main.post { emit("scan_failed", mapOf("code" to errorCode)) }.let { }
            }
            // Filtered on the service: Android stops unfiltered scans when the screen goes off.
            val filter = ScanFilter.Builder().setServiceUuid(ParcelUuid(SERVICE)).build()
            scanner.startScan(listOf(filter), ScanSettings.Builder().setScanMode(ScanSettings.SCAN_MODE_LOW_LATENCY).build(), cb)
            scanning = cb
        }
        done(null)
    }

    private fun serve(advertiser: android.bluetooth.le.BluetoothLeAdvertiser): String? {
        val message = BluetoothGattCharacteristic(
            MESSAGE,
            BluetoothGattCharacteristic.PROPERTY_READ or BluetoothGattCharacteristic.PROPERTY_WRITE,
            BluetoothGattCharacteristic.PERMISSION_READ or BluetoothGattCharacteristic.PERMISSION_WRITE,
        )
        // The server kept from one start to the next (stop only stops the advertisement): a new
        // service makes Android tell the paired devices their copy of this phone's services is
        // stale, which held the link with a computer 30 s (real test of 7 November).
        if (server == null) {
            val s = manager?.openGattServer(app, serverCallback) ?: return "no_gatt_server"
            s.addService(BluetoothGattService(SERVICE, BluetoothGattService.SERVICE_TYPE_PRIMARY).apply { addCharacteristic(message) })
            server = s
        }
        // The UUID alone (BLE-02): no name; the address is the system's random one. In the scan
        // response, one byte, the same on every phone: this device connects to the computers it
        // finds (HOLDS), which then wait for it rather than connect to it (J8).
        val data = AdvertiseData.Builder().setIncludeDeviceName(false).setIncludeTxPowerLevel(false)
            .addServiceUuid(ParcelUuid(SERVICE)).build()
        val response = AdvertiseData.Builder().setIncludeDeviceName(false).setIncludeTxPowerLevel(false)
            .addServiceData(ParcelUuid(SERVICE), byteArrayOf(HOLDS)).build()
        val settings = AdvertiseSettings.Builder()
            .setAdvertiseMode(AdvertiseSettings.ADVERTISE_MODE_LOW_LATENCY)
            .setTxPowerLevel(AdvertiseSettings.ADVERTISE_TX_POWER_MEDIUM)
            .setConnectable(true).setTimeout(0).build()
        val cb = object : AdvertiseCallback() {
            override fun onStartFailure(errorCode: Int) = main.post { emit("advertise_failed", mapOf("code" to errorCode)) }.let { }
        }
        advertiser.startAdvertising(settings, data, response, cb)
        advertising = cb
        return null
    }

    fun stop() {
        advertising?.let { manager?.adapter?.bluetoothLeAdvertiser?.stopAdvertising(it) }
        advertising = null
        scanning?.let { manager?.adapter?.bluetoothLeScanner?.stopScan(it) }
        scanning = null
        writes.clear()
        replies.clear()
        parked.clear()
        clientsOf.clear()
        seenAt.clear()
        for (c in clients.values.toList()) c.close("stopped")
        clients.clear()
    }

    // A device that advertises the service: said when new, then at most every 2 s.
    private fun seen(r: ScanResult) {
        val address = r.device.address
        val now = SystemClock.elapsedRealtime()
        val last = seenAt[address]
        if (last != null && now - last < SEEN_EVERY) return
        seenAt[address] = now
        emit("seen", mapOf("device" to address, "rssi" to r.rssi))
    }

    /** The answer [conn] reads next; a read already waiting gets it now. A held link's goes back to its computer. */
    fun reply(conn: String, data: ByteArray) {
        if (conn.startsWith(HELD)) return clients[conn.removePrefix(HELD)]?.answerCall(data) ?: Unit
        replies[conn] = data
        val device = clientsOf[conn] ?: return
        for ((request, offset) in parked.remove(conn) ?: return) answerRead(device, request, offset, data)
    }

    private fun answerRead(device: BluetoothDevice, request: Int, offset: Int, data: ByteArray) {
        val part = if (offset < data.size) data.copyOfRange(offset, data.size) else ByteArray(0)
        server?.sendResponse(device, request, BluetoothGatt.GATT_SUCCESS, offset, part)
    }

    // A whole message written by a client: a new exchange step, its answer to come from the core.
    private fun received(device: BluetoothDevice, data: ByteArray) {
        replies.remove(device.address)
        emit("message", mapOf("conn" to device.address, "data" to data))
    }

    private val serverCallback = object : BluetoothGattServerCallback() {
        override fun onConnectionStateChange(device: BluetoothDevice, status: Int, newState: Int) = main.post {
            val conn = device.address
            if (newState == BluetoothProfile.STATE_CONNECTED) {
                clientsOf[conn] = device
            } else if (newState == BluetoothProfile.STATE_DISCONNECTED) {
                clientsOf.remove(conn)
                writes.remove(conn)
                replies.remove(conn)
                parked.remove(conn)
                emit("closed", mapOf("conn" to conn))
            }
        }.let { }

        override fun onCharacteristicWriteRequest(
            device: BluetoothDevice, requestId: Int, characteristic: BluetoothGattCharacteristic,
            preparedWrite: Boolean, responseNeeded: Boolean, offset: Int, value: ByteArray,
        ) = main.post {
            clientsOf[device.address] = device
            if (characteristic.uuid != MESSAGE || offset > MAX_MESSAGE || value.size > MAX_MESSAGE) {
                if (responseNeeded) server?.sendResponse(device, requestId, BluetoothGatt.GATT_INVALID_OFFSET, offset, null)
                return@post
            }
            if (preparedWrite) {
                // A long write, in pieces: kept until the client executes it.
                val buffer = writes.getOrPut(device.address) { ByteArrayOutputStream() }
                if (offset != buffer.size() || buffer.size() + value.size > MAX_MESSAGE) {
                    writes.remove(device.address)
                    if (responseNeeded) server?.sendResponse(device, requestId, BluetoothGatt.GATT_INVALID_OFFSET, offset, null)
                    return@post
                }
                buffer.write(value)
                if (responseNeeded) server?.sendResponse(device, requestId, BluetoothGatt.GATT_SUCCESS, offset, value)
                return@post
            }
            if (responseNeeded) server?.sendResponse(device, requestId, BluetoothGatt.GATT_SUCCESS, offset, null)
            received(device, value)
        }.let { }

        override fun onExecuteWrite(device: BluetoothDevice, requestId: Int, execute: Boolean) = main.post {
            val buffer = writes.remove(device.address)
            server?.sendResponse(device, requestId, BluetoothGatt.GATT_SUCCESS, 0, null)
            if (execute && buffer != null) received(device, buffer.toByteArray())
        }.let { }

        override fun onCharacteristicReadRequest(device: BluetoothDevice, requestId: Int, offset: Int, characteristic: BluetoothGattCharacteristic) = main.post {
            clientsOf[device.address] = device
            val reply = replies[device.address]
            if (reply != null) return@post answerRead(device, requestId, offset, reply)
            // The core has not answered yet (a network being created): the read waits, under the
            // 30 s of a GATT transaction (BLE-11).
            parked.getOrPut(device.address) { mutableListOf() }.add(requestId to offset)
            main.postDelayed({
                val waiting = parked[device.address] ?: return@postDelayed
                if (waiting.remove(requestId to offset)) server?.sendResponse(device, requestId, BluetoothGatt.GATT_FAILURE, offset, null)
            }, ANSWER_WITHIN)
        }.let { }
    }

    /** Writes [message] to [address]'s service and reads its answer; connects first when needed. */
    fun exchange(address: String, message: ByteArray, done: (ByteArray?, String?) -> Unit) {
        val a = manager?.adapter ?: return done(null, "unsupported")
        if (!a.isEnabled) return done(null, "bluetooth_off")
        val c = clients.getOrPut(address) { Client(address) }
        c.send(message, done)
    }

    fun disconnect(address: String) {
        clients.remove(address)?.close("disconnected")
    }

    /** Done with [address] for now: its link closes, unless it is held (a computer's, J8). */
    fun release(address: String) {
        val c = clients[address] ?: return
        if (!c.held) disconnect(address)
    }

    // One GATT connection to another device's service. Its operations, one at a time as Android
    // wants them, in a queue: this device's exchange (a write, then a read of the answer) and,
    // on a held link, the computer's calls.
    private inner class Client(val address: String) : BluetoothGattCallback() {
        private var gatt: BluetoothGatt? = null
        private var message: BluetoothGattCharacteristic? = null
        private var call: BluetoothGattCharacteristic? = null
        private var ready = false // services discovered
        var held = false // subscribed to a computer's calls
        private var done: ((ByteArray?, String?) -> Unit)? = null
        private var timeout: Runnable? = null

        // An operation: started on the link (false: refused at once), then ended by its callback
        // with the value read and the status.
        private inner class Op(val start: (BluetoothGatt) -> Boolean, val end: (ByteArray?, Int) -> Unit)
        private val ops = ArrayDeque<Op>()
        private var running: Op? = null

        private fun enqueue(op: Op) {
            ops.addLast(op)
            next()
        }

        private fun next() {
            if (running != null || !ready) return
            val g = gatt ?: return
            val op = ops.removeFirstOrNull() ?: return
            running = op
            if (!op.start(g)) {
                running = null
                op.end(null, -1)
                next()
            }
        }

        private fun complete(value: ByteArray?, status: Int) {
            val op = running ?: return
            running = null
            op.end(value, status)
            next()
        }

        fun send(m: ByteArray, cb: (ByteArray?, String?) -> Unit) {
            if (done != null) return cb(null, "busy")
            done = cb
            val t = Runnable { close("timeout") }
            timeout = t
            main.postDelayed(t, ANSWER_WITHIN)
            // No MTU asked: it cost 3.3 to 3.7 s towards Windows (WP-D.0). A message longer than
            // the default MTU goes as a long write, in pieces, and the answer as a long read.
            enqueue(Op({ g -> write(g, message, m) }) { _, status ->
                if (status != BluetoothGatt.GATT_SUCCESS) return@Op finish(null, if (status == -1) "write_failed" else "write_status:$status")
                enqueue(Op({ g -> message?.let { g.readCharacteristic(it) } ?: false }) { value, st ->
                    if (st != BluetoothGatt.GATT_SUCCESS) finish(null, if (st == -1) "read_failed" else "read_status:$st") else finish(value ?: ByteArray(0), null)
                })
            })
            if (gatt == null) {
                val device = manager!!.adapter.getRemoteDevice(address)
                gatt = device.connectGatt(app, false, this, BluetoothDevice.TRANSPORT_LE)
            }
        }

        // The computer's call: its message read, given to the core as a message of "held:" and
        // its address; the core's answer comes back by answerCall.
        private fun called() {
            enqueue(Op({ g -> call?.let { g.readCharacteristic(it) } ?: false }) { value, status ->
                if (status != BluetoothGatt.GATT_SUCCESS || value == null || value.isEmpty()) return@Op
                emit("message", mapOf("conn" to HELD + address, "data" to value))
            })
        }

        /** The core's answer to the computer's call, then a read that marks its end. */
        fun answerCall(data: ByteArray) {
            enqueue(Op({ g -> write(g, call, data) }) { _, status ->
                if (status == BluetoothGatt.GATT_SUCCESS) enqueue(Op({ g -> call?.let { g.readCharacteristic(it) } ?: false }) { _, _ -> })
            })
        }

        private fun write(g: BluetoothGatt, c: BluetoothGattCharacteristic?, value: ByteArray): Boolean {
            c ?: return false
            return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                g.writeCharacteristic(c, value, BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT) == BluetoothGatt.GATT_SUCCESS
            } else {
                @Suppress("DEPRECATION")
                c.value = value
                c.writeType = BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT
                @Suppress("DEPRECATION")
                g.writeCharacteristic(c)
            }
        }

        // Subscribed to the computer's calls: the link is kept, the computer told by its own
        // Bluetooth, and Dart by "held".
        private fun hold() {
            val c = call ?: return
            enqueue(Op({ g ->
                val cccd = c.getDescriptor(CCCD) ?: return@Op false
                if (!g.setCharacteristicNotification(c, true)) return@Op false
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                    g.writeDescriptor(cccd, android.bluetooth.BluetoothGattDescriptor.ENABLE_NOTIFICATION_VALUE) == BluetoothGatt.GATT_SUCCESS
                } else {
                    @Suppress("DEPRECATION")
                    cccd.value = android.bluetooth.BluetoothGattDescriptor.ENABLE_NOTIFICATION_VALUE
                    @Suppress("DEPRECATION")
                    g.writeDescriptor(cccd)
                }
            }) { _, status ->
                if (status != BluetoothGatt.GATT_SUCCESS || gatt == null) return@Op
                held = true
                emit("held", mapOf("conn" to address, "held" to true))
                // A call made before this phone listened: its notification lost, its message
                // still there to read (a paired computer sees the subscription before it is made,
                // real test of 18 November 2026).
                called()
            })
        }

        private fun finish(answer: ByteArray?, error: String?) {
            timeout?.let { main.removeCallbacks(it) }
            timeout = null
            val cb = done
            done = null
            cb?.invoke(answer, error)
        }

        fun close(why: String) {
            finish(null, why)
            ops.clear()
            running = null
            gatt?.let {
                it.disconnect()
                it.close()
            }
            gatt = null
            message = null
            call = null
            ready = false
            if (held) {
                held = false
                emit("held", mapOf("conn" to address, "held" to false))
                emit("closed", mapOf("conn" to HELD + address))
            }
            if (clients[address] === this) clients.remove(address)
        }

        override fun onConnectionStateChange(g: BluetoothGatt, status: Int, newState: Int) = main.post {
            if (newState == BluetoothProfile.STATE_CONNECTED) {
                // A full discovery first: asking for one service straight away failed against
                // Windows (WP-D.0).
                g.discoverServices()
            } else if (newState == BluetoothProfile.STATE_DISCONNECTED) {
                close("disconnected:$status")
            }
        }.let { }

        override fun onServicesDiscovered(g: BluetoothGatt, status: Int) = main.post {
            val service = g.getService(SERVICE)
            message = service?.getCharacteristic(MESSAGE)
            call = service?.getCharacteristic(CALL)
            if (message == null) return@post close("no_service")
            ready = true
            next() // this device's exchange first
            hold() // a computer's: kept, subscribed
        }.let { }

        override fun onCharacteristicWrite(g: BluetoothGatt, c: BluetoothGattCharacteristic, status: Int) = main.post {
            complete(null, status)
        }.let { }

        override fun onCharacteristicRead(g: BluetoothGatt, c: BluetoothGattCharacteristic, value: ByteArray, status: Int) =
            main.post { complete(value, status) }.let { }

        @Deprecated("Android 12 and earlier")
        @Suppress("DEPRECATION")
        override fun onCharacteristicRead(g: BluetoothGatt, c: BluetoothGattCharacteristic, status: Int) {
            val value = c.value ?: ByteArray(0)
            main.post { complete(value, status) }
        }

        override fun onDescriptorWrite(g: BluetoothGatt, d: android.bluetooth.BluetoothGattDescriptor, status: Int) =
            main.post { complete(null, status) }.let { }

        override fun onCharacteristicChanged(g: BluetoothGatt, c: BluetoothGattCharacteristic, value: ByteArray) =
            main.post { if (c.uuid == CALL) called() }.let { }

        @Deprecated("Android 12 and earlier")
        @Suppress("DEPRECATION")
        override fun onCharacteristicChanged(g: BluetoothGatt, c: BluetoothGattCharacteristic) {
            main.post { if (c.uuid == CALL) called() }
        }
    }

    companion object {
        // Bible §6.6, BLE-02: medXfer's service and its one characteristic.
        val SERVICE: UUID = UUID.fromString("c46b12c3-1dee-482d-a490-b185212312fb")
        val MESSAGE: UUID = UUID.fromString("c46b12c4-1dee-482d-a490-b185212312fb")
        // A computer's call characteristic (J8): its notification calls this phone, which reads
        // the computer's message there and writes its answer back.
        val CALL: UUID = UUID.fromString("c46b12c5-1dee-482d-a490-b185212312fb")
        private val CCCD: UUID = UUID.fromString("00002902-0000-1000-8000-00805f9b34fb")
        private const val HELD = "held:"
        private const val HOLDS: Byte = 2 // the scan response's byte: this device connects to computers
        private const val MAX_MESSAGE = 512 // BLE-03
        private const val ANSWER_WITHIN = 25_000L // BLE-11: under the 30 s of a GATT transaction
        private const val SEEN_EVERY = 2_000L
    }
}
