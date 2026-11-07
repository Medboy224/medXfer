package dev.medxfer.app

import android.annotation.SuppressLint
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
import android.content.Context
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

    // The scan.
    private var scanning: ScanCallback? = null
    private val seenAt = mutableMapOf<String, Long>()

    // The client side: one GATT connection per device reached.
    private val clients = mutableMapOf<String, Client>()

    /** Advertises and answers ([advertise]), scans ([scan]). done gets an error, or null. */
    fun start(advertise: Boolean, scan: Boolean, done: (String?) -> Unit) {
        val a = manager?.adapter ?: return done("unsupported")
        if (!a.isEnabled) return done("bluetooth_off")
        if (advertise && server == null) {
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
        val s = manager?.openGattServer(app, serverCallback) ?: return "no_gatt_server"
        s.addService(BluetoothGattService(SERVICE, BluetoothGattService.SERVICE_TYPE_PRIMARY).apply { addCharacteristic(message) })
        server = s
        // The UUID alone (BLE-02): no name, no service data; the address is the system's random one.
        val data = AdvertiseData.Builder().setIncludeDeviceName(false).setIncludeTxPowerLevel(false)
            .addServiceUuid(ParcelUuid(SERVICE)).build()
        val settings = AdvertiseSettings.Builder()
            .setAdvertiseMode(AdvertiseSettings.ADVERTISE_MODE_LOW_LATENCY)
            .setTxPowerLevel(AdvertiseSettings.ADVERTISE_TX_POWER_MEDIUM)
            .setConnectable(true).setTimeout(0).build()
        val cb = object : AdvertiseCallback() {
            override fun onStartFailure(errorCode: Int) = main.post { emit("advertise_failed", mapOf("code" to errorCode)) }.let { }
        }
        advertiser.startAdvertising(settings, data, cb)
        advertising = cb
        return null
    }

    fun stop() {
        advertising?.let { manager?.adapter?.bluetoothLeAdvertiser?.stopAdvertising(it) }
        advertising = null
        server?.close()
        server = null
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

    /** The answer [conn] reads next; a read already waiting gets it now. */
    fun reply(conn: String, data: ByteArray) {
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

    // One GATT connection to another device's service: one exchange at a time.
    private inner class Client(val address: String) : BluetoothGattCallback() {
        private var gatt: BluetoothGatt? = null
        private var characteristic: BluetoothGattCharacteristic? = null
        private var pending: ByteArray? = null
        private var done: ((ByteArray?, String?) -> Unit)? = null
        private var timeout: Runnable? = null

        fun send(message: ByteArray, cb: (ByteArray?, String?) -> Unit) {
            if (done != null) return cb(null, "busy")
            pending = message
            done = cb
            val t = Runnable { close("timeout") }
            timeout = t
            main.postDelayed(t, ANSWER_WITHIN)
            if (characteristic != null) return write()
            if (gatt == null) {
                val device = manager!!.adapter.getRemoteDevice(address)
                gatt = device.connectGatt(app, false, this, BluetoothDevice.TRANSPORT_LE)
            }
        }

        private fun write() {
            val g = gatt ?: return
            val c = characteristic ?: return
            val message = pending ?: return
            // No MTU asked: it cost 3.3 to 3.7 s towards Windows (WP-D.0). A message longer than
            // the default MTU goes as a long write, in pieces, and the answer as a long read.
            val ok = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                g.writeCharacteristic(c, message, BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT) == BluetoothGatt.GATT_SUCCESS
            } else {
                @Suppress("DEPRECATION")
                c.value = message
                c.writeType = BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT
                @Suppress("DEPRECATION")
                g.writeCharacteristic(c)
            }
            if (!ok) finish(null, "write_failed")
        }

        private fun finish(answer: ByteArray?, error: String?) {
            timeout?.let { main.removeCallbacks(it) }
            timeout = null
            pending = null
            val cb = done
            done = null
            cb?.invoke(answer, error)
        }

        fun close(why: String) {
            finish(null, why)
            gatt?.let {
                it.disconnect()
                it.close()
            }
            gatt = null
            characteristic = null
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
            characteristic = g.getService(SERVICE)?.getCharacteristic(MESSAGE)
            if (characteristic == null) close("no_service") else write()
        }.let { }

        override fun onCharacteristicWrite(g: BluetoothGatt, c: BluetoothGattCharacteristic, status: Int) = main.post {
            if (status != BluetoothGatt.GATT_SUCCESS) return@post finish(null, "write_status:$status")
            if (!g.readCharacteristic(c)) finish(null, "read_failed")
        }.let { }

        override fun onCharacteristicRead(g: BluetoothGatt, c: BluetoothGattCharacteristic, value: ByteArray, status: Int) =
            main.post { read(value, status) }.let { }

        @Deprecated("Android 12 and earlier")
        @Suppress("DEPRECATION")
        override fun onCharacteristicRead(g: BluetoothGatt, c: BluetoothGattCharacteristic, status: Int) {
            val value = c.value ?: ByteArray(0)
            main.post { read(value, status) }
        }

        private fun read(value: ByteArray, status: Int) {
            if (status != BluetoothGatt.GATT_SUCCESS) finish(null, "read_status:$status") else finish(value, null)
        }
    }

    companion object {
        // Bible §6.6, BLE-02: medXfer's service and its one characteristic.
        val SERVICE: UUID = UUID.fromString("c46b12c3-1dee-482d-a490-b185212312fb")
        val MESSAGE: UUID = UUID.fromString("c46b12c4-1dee-482d-a490-b185212312fb")
        private const val MAX_MESSAGE = 512 // BLE-03
        private const val ANSWER_WITHIN = 25_000L // BLE-11: under the 30 s of a GATT transaction
        private const val SEEN_EVERY = 2_000L
    }
}
