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
import android.bluetooth.le.ScanResult
import android.bluetooth.le.ScanSettings
import android.content.Context
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.ParcelUuid
import android.os.SystemClock
import java.util.UUID

// The BLE probe of WP-D.0 (J6d plan), driven by adb like WifiDirectGroup: both roles, what fits
// in the advertisement, the delays of an exchange of about 200 bytes. System APIs only; the app
// does not use it. The UUIDs are the probe's (the same in ble-probe.exe, its Windows side).
@SuppressLint("MissingPermission") // the Bluetooth permissions are granted by adb for the probe
class BleProbe(context: Context) {
    private val context = context.applicationContext
    private val manager = context.getSystemService(BluetoothManager::class.java)
    private val main = Handler(Looper.getMainLooper())
    private var server: BluetoothGattServer? = null
    private var advertising: AdvertiseCallback? = null
    private var scanning: ScanCallback? = null

    fun caps(done: Done) {
        val a = manager?.adapter ?: return done(null, "no Bluetooth adapter")
        done(
            mapOf(
                "enabled" to a.isEnabled,
                "multipleAdvertisement" to a.isMultipleAdvertisementSupported,
                "extendedAdvertising" to a.isLeExtendedAdvertisingSupported,
                "maxAdvertisingData" to a.leMaximumAdvertisingDataLength,
                "phy2M" to a.isLe2MPhySupported,
                "phyCoded" to a.isLeCodedPhySupported,
            ),
            null,
        )
    }

    /** Advertises the service for [seconds] and answers writes and reads with [reply] bytes. */
    fun serve(seconds: Int, reply: Int, done: Done) {
        val a = manager?.adapter ?: return done(null, "no Bluetooth adapter")
        val advertiser = a.bluetoothLeAdvertiser ?: return done(null, "no advertiser (Bluetooth off?)")
        stop()
        val t0 = SystemClock.elapsedRealtime()
        val ms = { SystemClock.elapsedRealtime() - t0 }
        val answer = ByteArray(reply) { (0x80 + it).toByte() }
        val message = BluetoothGattCharacteristic(
            MESSAGE,
            BluetoothGattCharacteristic.PROPERTY_READ or BluetoothGattCharacteristic.PROPERTY_WRITE,
            BluetoothGattCharacteristic.PERMISSION_READ or BluetoothGattCharacteristic.PERMISSION_WRITE,
        )
        var srv: BluetoothGattServer? = null
        srv = manager.openGattServer(context, object : BluetoothGattServerCallback() {
            override fun onConnectionStateChange(device: BluetoothDevice, status: Int, newState: Int) =
                done(mapOf("ms" to ms(), "device" to device.address, "connected" to (newState == BluetoothProfile.STATE_CONNECTED)), null)

            override fun onMtuChanged(device: BluetoothDevice, mtu: Int) = done(mapOf("ms" to ms(), "mtu" to mtu), null)

            override fun onCharacteristicWriteRequest(
                device: BluetoothDevice, requestId: Int, characteristic: BluetoothGattCharacteristic,
                preparedWrite: Boolean, responseNeeded: Boolean, offset: Int, value: ByteArray,
            ) {
                done(mapOf("ms" to ms(), "write" to value.size, "offset" to offset, "prepared" to preparedWrite), null)
                if (responseNeeded) srv?.sendResponse(device, requestId, BluetoothGatt.GATT_SUCCESS, offset, value)
            }

            override fun onExecuteWrite(device: BluetoothDevice, requestId: Int, execute: Boolean) {
                srv?.sendResponse(device, requestId, BluetoothGatt.GATT_SUCCESS, 0, null)
            }

            override fun onCharacteristicReadRequest(device: BluetoothDevice, requestId: Int, offset: Int, characteristic: BluetoothGattCharacteristic) {
                done(mapOf("ms" to ms(), "read" to answer.size, "offset" to offset), null)
                val part = if (offset < answer.size) answer.copyOfRange(offset, answer.size) else ByteArray(0)
                srv?.sendResponse(device, requestId, BluetoothGatt.GATT_SUCCESS, offset, part)
            }
        })
        val s = srv ?: return done(null, "no GATT server")
        s.addService(BluetoothGattService(SERVICE, BluetoothGattService.SERVICE_TYPE_PRIMARY).apply { addCharacteristic(message) })
        server = s
        // The UUID inside the service data (flags 3 + 2 + 16 + 10 = 31 bytes), and the UUID list in
        // the scan response: a scanner sees the service either way.
        val data = AdvertiseData.Builder().setIncludeDeviceName(false)
            .addServiceData(ParcelUuid(SERVICE), ByteArray(10) { (0x10 + it).toByte() }).build()
        val response = AdvertiseData.Builder().addServiceUuid(ParcelUuid(SERVICE)).build()
        val settings = AdvertiseSettings.Builder()
            .setAdvertiseMode(AdvertiseSettings.ADVERTISE_MODE_LOW_LATENCY)
            .setTxPowerLevel(AdvertiseSettings.ADVERTISE_TX_POWER_MEDIUM)
            .setConnectable(true).setTimeout(0).build()
        val cb = object : AdvertiseCallback() {
            override fun onStartSuccess(settingsInEffect: AdvertiseSettings) = done(mapOf("ms" to ms(), "advertising" to "started"), null)
            override fun onStartFailure(errorCode: Int) = done(null, "advertising failed: $errorCode")
        }
        advertiser.startAdvertising(settings, data, response, cb)
        advertising = cb
        main.postDelayed({ if (advertising === cb) { stop(); done(mapOf("ms" to ms(), "served" to seconds), null) } }, seconds * 1000L)
    }

    /** Lists the devices that advertise the service, for [seconds]; [take] stops at the one it accepts. */
    fun scan(seconds: Int, done: Done, take: ((ScanResult) -> Boolean)? = null) {
        val scanner = manager?.adapter?.bluetoothLeScanner ?: return done(null, "no scanner (Bluetooth off?)")
        stopScan()
        val t0 = SystemClock.elapsedRealtime()
        val all = mutableSetOf<String>()
        val ours = mutableSetOf<String>()
        val cb = object : ScanCallback() {
            override fun onScanResult(callbackType: Int, r: ScanResult) {
                all += r.device.address
                val record = r.scanRecord ?: return
                val medxfer = record.serviceUuids?.contains(ParcelUuid(SERVICE)) == true || record.getServiceData(ParcelUuid(SERVICE)) != null
                if (!medxfer) return
                if (ours.add(r.device.address)) {
                    done(
                        mapOf(
                            "ms" to SystemClock.elapsedRealtime() - t0, "new" to r.device.address, "rssi" to r.rssi,
                            "connectable" to r.isConnectable, "sections" to sections(record.bytes),
                        ),
                        null,
                    )
                }
                if (take?.invoke(r) == true) { stopScan(); return }
            }

            override fun onScanFailed(errorCode: Int) = done(null, "scan failed: $errorCode")
        }
        scanner.startScan(null, ScanSettings.Builder().setScanMode(ScanSettings.SCAN_MODE_LOW_LATENCY).build(), cb)
        scanning = cb
        main.postDelayed({ if (scanning === cb) { stopScan(); done(mapOf("devices" to all.size, "medxfer" to ours.size), null) } }, seconds * 1000L)
    }

    /** Finds the device ([address], or the first one seen), then writes [bytes] and reads the answer. */
    fun exchange(address: String?, bytes: Int, done: Done) {
        val t0 = SystemClock.elapsedRealtime()
        val ms = { SystemClock.elapsedRealtime() - t0 }
        scan(20, done) { r ->
            if (address != null && !r.device.address.equals(address, ignoreCase = true)) return@scan false
            val times = mutableMapOf<String, Any?>("seen" to ms())
            val payload = ByteArray(bytes) { (1 + it).toByte() }
            r.device.connectGatt(context, false, object : BluetoothGattCallback() {
                var finished = false

                override fun onConnectionStateChange(g: BluetoothGatt, status: Int, newState: Int) {
                    if (newState == BluetoothProfile.STATE_CONNECTED) {
                        times["connected"] = ms()
                        g.requestMtu(517)
                    } else if (newState == BluetoothProfile.STATE_DISCONNECTED) {
                        g.close()
                        if (!finished) done(times, "disconnected, status $status")
                    }
                }

                override fun onMtuChanged(g: BluetoothGatt, mtu: Int, status: Int) {
                    times["mtu"] = mtu
                    times["mtuAt"] = ms()
                    g.discoverServices()
                }

                override fun onServicesDiscovered(g: BluetoothGatt, status: Int) {
                    times["services"] = ms()
                    val c = g.getService(SERVICE)?.getCharacteristic(MESSAGE) ?: run { finished = true; g.disconnect(); return done(times, "no medXfer service") }
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                        g.writeCharacteristic(c, payload, BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT)
                    } else {
                        @Suppress("DEPRECATION")
                        c.value = payload
                        c.writeType = BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT
                        @Suppress("DEPRECATION")
                        g.writeCharacteristic(c)
                    }
                }

                override fun onCharacteristicWrite(g: BluetoothGatt, c: BluetoothGattCharacteristic, status: Int) {
                    times["written"] = ms()
                    times["writeStatus"] = status
                    g.readCharacteristic(c)
                }

                override fun onCharacteristicRead(g: BluetoothGatt, c: BluetoothGattCharacteristic, value: ByteArray, status: Int) = read(g, value, status)

                @Deprecated("Android 12 and earlier")
                @Suppress("DEPRECATION")
                override fun onCharacteristicRead(g: BluetoothGatt, c: BluetoothGattCharacteristic, status: Int) = read(g, c.value ?: ByteArray(0), status)

                fun read(g: BluetoothGatt, value: ByteArray, status: Int) {
                    times["read"] = ms()
                    times["readBytes"] = value.size
                    times["readStatus"] = status
                    finished = true
                    done(times, null)
                    g.disconnect()
                }
            }, BluetoothDevice.TRANSPORT_LE)
            true
        }
    }

    /**
     * The phone always connects (J8): scans for [seconds]; at each fresh token in the call
     * probe's service data (`01 ca` and 4 bytes, ble-probe call), connects to that computer,
     * writes the token back, reads its message, writes a reply of 200 bytes and reads again,
     * with the delays from the advertisement seen.
     */
    fun callee(seconds: Int, done: Done) {
        val scanner = manager?.adapter?.bluetoothLeScanner ?: return done(null, "no scanner (Bluetooth off?)")
        stopScan()
        var last: String? = null
        var busy = false
        var pending: ByteArray? = null
        var pendingAt = 0L
        val cb = object : ScanCallback() {
            val logged = mutableSetOf<String>()

            override fun onScanResult(callbackType: Int, r: ScanResult) {
                val rec = r.scanRecord
                val call = ParcelUuid(CALL_SERVICE)
                // Any 128-bit service data (0x21) from any device, once: does the token go out?
                if (rec != null && rec.bytes != null && sections(rec.bytes).contains("21:") && logged.add("sd" + r.device.address)) {
                    done(mapOf("withServiceData" to r.device.address, "sections" to sections(rec.bytes), "keys" to rec.serviceData?.keys?.joinToString()), null)
                }
                if (rec != null && (rec.serviceUuids?.contains(call) == true || rec.serviceData?.containsKey(call) == true) &&
                    logged.add(r.device.address + (rec.getServiceData(call)?.size ?: -1))
                ) {
                    done(mapOf("sighted" to r.device.address, "sections" to sections(rec.bytes), "data" to rec.getServiceData(call)?.joinToString("") { String.format("%02x", it.toInt() and 0xff) }), null)
                }
                if (rec == null || busy) return
                val data = rec.getServiceData(call)
                if (data != null && data.size >= 5 && data[0] == 0xca.toByte()) {
                    // The token: its computer advertises its service again, from a new address.
                    val hex = data.copyOfRange(0, 5).joinToString("") { String.format("%02x", it.toInt() and 0xff) }
                    if (hex != last) {
                        last = hex
                        pending = data.copyOfRange(0, 5)
                        pendingAt = SystemClock.elapsedRealtime()
                        done(mapOf("token" to hex, "from" to r.device.address), null)
                    }
                    return
                }
                val token = pending ?: return
                if (!r.isConnectable || rec.serviceUuids?.contains(call) != true) return
                if (SystemClock.elapsedRealtime() - pendingAt > 5000) { pending = null; return }
                pending = null
                busy = true
                done(mapOf("service" to r.device.address, "afterToken" to SystemClock.elapsedRealtime() - pendingAt), null)
                answer(r.device, token, last ?: "", done) { busy = false }
            }

            override fun onScanFailed(errorCode: Int) = done(null, "scan failed: $errorCode")
        }
        // Unfiltered, the screen on (Android stops such a scan with it off): the token's
        // advertisement carries the call service in its data only. Result, 18 November 2026: an
        // Intel card sends no second advertisement while a GATT service exists, the token never
        // arrives; the held link (holder) replaced this way of calling.
        scanner.startScan(null, ScanSettings.Builder().setScanMode(ScanSettings.SCAN_MODE_LOW_LATENCY).build(), cb)
        scanning = cb
        done(mapOf("callee" to "scanning", "seconds" to seconds), null)
        main.postDelayed({ if (scanning === cb) { stopScan(); done(mapOf("callee" to "stopped"), null) } }, seconds * 1000L)
    }

    /**
     * The phone always connects and keeps its link (J8): the first computer advertising the call
     * service, connected to once, notifications turned on; each notification (`ca` and 4 bytes,
     * ble-probe hold) answered over that link: the token written back, a message read, a reply
     * of 200 bytes written, read again. The delays from the notification.
     */
    fun holder(seconds: Int, done: Done) {
        val scanner = manager?.adapter?.bluetoothLeScanner ?: return done(null, "no scanner (Bluetooth off?)")
        stopScan()
        var connecting = false
        val t0 = SystemClock.elapsedRealtime()
        val ms = { SystemClock.elapsedRealtime() - t0 }
        val cb = object : ScanCallback() {
            override fun onScanResult(callbackType: Int, r: ScanResult) {
                if (connecting || !r.isConnectable || r.scanRecord?.serviceUuids?.contains(ParcelUuid(CALL_SERVICE)) != true) return
                connecting = true
                stopScan()
                done(mapOf("seen" to r.device.address, "ms" to ms()), null)
                hold(r.device, seconds, done)
            }

            override fun onScanFailed(errorCode: Int) = done(null, "scan failed: $errorCode")
        }
        val filters = listOf(android.bluetooth.le.ScanFilter.Builder().setServiceUuid(ParcelUuid(CALL_SERVICE)).build())
        scanner.startScan(filters, ScanSettings.Builder().setScanMode(ScanSettings.SCAN_MODE_LOW_LATENCY).build(), cb)
        scanning = cb
        done(mapOf("holder" to "scanning", "seconds" to seconds), null)
    }

    private fun hold(device: BluetoothDevice, seconds: Int, done: Done) {
        val t0 = SystemClock.elapsedRealtime()
        val ms = { SystemClock.elapsedRealtime() - t0 }
        val reply = ByteArray(200) { (1 + it).toByte() }
        device.connectGatt(context, false, object : BluetoothGattCallback() {
            var c: BluetoothGattCharacteristic? = null
            var step = -1 // -1 idle; 0 token written, 1 message read, 2 reply written, 3 read again
            var calledAt = 0L
            val times = mutableMapOf<String, Any?>()
            fun since() = SystemClock.elapsedRealtime() - calledAt

            fun write(g: BluetoothGatt, value: ByteArray) {
                val ch = c ?: return
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                    g.writeCharacteristic(ch, value, BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT)
                } else {
                    @Suppress("DEPRECATION")
                    ch.value = value
                    ch.writeType = BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT
                    @Suppress("DEPRECATION")
                    g.writeCharacteristic(ch)
                }
            }

            override fun onConnectionStateChange(g: BluetoothGatt, status: Int, newState: Int) {
                if (newState == BluetoothProfile.STATE_CONNECTED) {
                    done(mapOf("connected" to ms()), null)
                    g.requestMtu(517)
                    main.postDelayed({ g.disconnect() }, seconds * 1000L)
                } else if (newState == BluetoothProfile.STATE_DISCONNECTED) {
                    g.close()
                    done(mapOf("disconnected" to ms(), "status" to status), null)
                }
            }

            override fun onMtuChanged(g: BluetoothGatt, mtu: Int, status: Int) { g.discoverServices() }

            override fun onServicesDiscovered(g: BluetoothGatt, status: Int) {
                val ch = g.getService(CALL_SERVICE)?.getCharacteristic(CALL_MESSAGE) ?: return done(null, "no call service")
                c = ch
                g.setCharacteristicNotification(ch, true)
                val cccd = ch.getDescriptor(UUID.fromString("00002902-0000-1000-8000-00805f9b34fb"))
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                    g.writeDescriptor(cccd, android.bluetooth.BluetoothGattDescriptor.ENABLE_NOTIFICATION_VALUE)
                } else {
                    @Suppress("DEPRECATION")
                    cccd.value = android.bluetooth.BluetoothGattDescriptor.ENABLE_NOTIFICATION_VALUE
                    @Suppress("DEPRECATION")
                    g.writeDescriptor(cccd)
                }
            }

            override fun onDescriptorWrite(g: BluetoothGatt, d: android.bluetooth.BluetoothGattDescriptor, status: Int) =
                done(mapOf("subscribed" to ms(), "status" to status), null)

            override fun onCharacteristicChanged(g: BluetoothGatt, ch: BluetoothGattCharacteristic, value: ByteArray) = called(g, value)

            @Deprecated("Android 12 and earlier")
            @Suppress("DEPRECATION")
            override fun onCharacteristicChanged(g: BluetoothGatt, ch: BluetoothGattCharacteristic) = called(g, ch.value ?: ByteArray(0))

            fun called(g: BluetoothGatt, value: ByteArray) {
                if (value.size != 5 || value[0] != 0xca.toByte() || step != -1) return
                calledAt = SystemClock.elapsedRealtime()
                times.clear()
                times["token"] = value.joinToString("") { String.format("%02x", it.toInt() and 0xff) }
                step = 0
                write(g, value)
            }

            override fun onCharacteristicWrite(g: BluetoothGatt, ch: BluetoothGattCharacteristic, status: Int) {
                times[if (step == 0) "tokenWritten" else "replyWritten"] = since()
                step++
                g.readCharacteristic(ch)
            }

            override fun onCharacteristicRead(g: BluetoothGatt, ch: BluetoothGattCharacteristic, value: ByteArray, status: Int) = read(g, value)

            @Deprecated("Android 12 and earlier")
            @Suppress("DEPRECATION")
            override fun onCharacteristicRead(g: BluetoothGatt, ch: BluetoothGattCharacteristic, status: Int) = read(g, ch.value ?: ByteArray(0))

            fun read(g: BluetoothGatt, value: ByteArray) {
                times[if (step == 1) "messageRead" else "readAgain"] = since()
                step++
                if (step == 2) write(g, reply) else { step = -1; done(times.toMap(), null) }
            }
        }, BluetoothDevice.TRANSPORT_LE)
    }

    private fun answer(device: BluetoothDevice, token: ByteArray, hex: String, done: Done, finished: () -> Unit) {
        val t0 = SystemClock.elapsedRealtime()
        val ms = { SystemClock.elapsedRealtime() - t0 }
        val times = mutableMapOf<String, Any?>("token" to hex, "device" to device.address)
        val reply = ByteArray(200) { (1 + it).toByte() }
        var step = 0 // 0 token written, 1 message read, 2 reply written, 3 read again
        device.connectGatt(context, false, object : BluetoothGattCallback() {
            var c: BluetoothGattCharacteristic? = null
            var over = false

            fun end(g: BluetoothGatt, error: String?) {
                if (over) return
                over = true
                times["total"] = ms()
                done(times, error)
                g.disconnect()
            }

            fun write(g: BluetoothGatt, value: ByteArray) {
                val ch = c ?: return
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                    g.writeCharacteristic(ch, value, BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT)
                } else {
                    @Suppress("DEPRECATION")
                    ch.value = value
                    ch.writeType = BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT
                    @Suppress("DEPRECATION")
                    g.writeCharacteristic(ch)
                }
            }

            override fun onConnectionStateChange(g: BluetoothGatt, status: Int, newState: Int) {
                if (newState == BluetoothProfile.STATE_CONNECTED) {
                    times["connected"] = ms()
                    g.requestMtu(517)
                } else if (newState == BluetoothProfile.STATE_DISCONNECTED) {
                    g.close()
                    main.post { finished() }
                    if (!over) { over = true; done(times, "disconnected, status $status") }
                }
            }

            override fun onMtuChanged(g: BluetoothGatt, mtu: Int, status: Int) {
                times["mtu"] = ms()
                g.discoverServices()
            }

            override fun onServicesDiscovered(g: BluetoothGatt, status: Int) {
                times["services"] = ms()
                c = g.getService(CALL_SERVICE)?.getCharacteristic(CALL_MESSAGE) ?: return end(g, "no call service")
                write(g, token)
            }

            override fun onCharacteristicWrite(g: BluetoothGatt, ch: BluetoothGattCharacteristic, status: Int) {
                if (status != BluetoothGatt.GATT_SUCCESS) return end(g, "write status $status")
                times[if (step == 0) "tokenWritten" else "replyWritten"] = ms()
                step++
                g.readCharacteristic(ch)
            }

            override fun onCharacteristicRead(g: BluetoothGatt, ch: BluetoothGattCharacteristic, value: ByteArray, status: Int) = read(g, value, status)

            @Deprecated("Android 12 and earlier")
            @Suppress("DEPRECATION")
            override fun onCharacteristicRead(g: BluetoothGatt, ch: BluetoothGattCharacteristic, status: Int) = read(g, ch.value ?: ByteArray(0), status)

            fun read(g: BluetoothGatt, value: ByteArray, status: Int) {
                if (status != BluetoothGatt.GATT_SUCCESS) return end(g, "read status $status")
                times[if (step == 1) "messageRead" else "readAgain"] = ms()
                times["bytes$step"] = value.size
                step++
                if (step == 2) write(g, reply) else end(g, null)
            }
        }, BluetoothDevice.TRANSPORT_LE)
    }

    fun stop() {
        advertising?.let { manager?.adapter?.bluetoothLeAdvertiser?.stopAdvertising(it) }
        advertising = null
        server?.close()
        server = null
        stopScan()
    }

    private fun stopScan() {
        scanning?.let { manager?.adapter?.bluetoothLeScanner?.stopScan(it) }
        scanning = null
    }

    // The sections of a raw advertisement, "type:length" (01 flags, 07 128-bit UUIDs, 21 service data...).
    private fun sections(raw: ByteArray?): String {
        val b = raw ?: return ""
        val out = StringBuilder()
        var i = 0
        while (i < b.size) {
            val len = b[i].toInt() and 0xff
            if (len == 0 || i + 1 >= b.size) break
            out.append(String.format(" %02x:%d", b[i + 1].toInt() and 0xff, len - 1))
            i += len + 1
        }
        return out.toString().trim()
    }

    companion object {
        val SERVICE: UUID = UUID.fromString("c46b12c3-1dee-482d-a490-b185212312fb")
        val MESSAGE: UUID = UUID.fromString("c46b12c4-1dee-482d-a490-b185212312fb")
        val CALL_SERVICE: UUID = UUID.fromString("c46b12d3-1dee-482d-a490-b185212312fb")
        val CALL_MESSAGE: UUID = UUID.fromString("c46b12d4-1dee-482d-a490-b185212312fb")
    }
}

typealias Done = (Map<String, Any?>?, String?) -> Unit
