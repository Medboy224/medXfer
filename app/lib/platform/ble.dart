/// Bluetooth Low Energy, for the path by BLE (bible §6.6, WP-D.3 of the J6d plan): the platform
/// advertises medXfer's service, answers the devices that connect to it, finds the others, and
/// carries a message to one of them. It only carries bytes: the core makes and reads every
/// message (ble_server, ble_client). One adapter per system and API (D5); none where the device
/// has no BLE (Zorin, D4): the QR code and the PIN remain.
library;

import 'dart:async';
import 'dart:io';

import 'package:flutter/services.dart';

/// What the radio tells.
sealed class BleEvent {
  const BleEvent();
}

/// A device that advertises medXfer's service. Its address changes now and then (privacy): the
/// recognition (ble_client hello) says who it is.
class BleSeen extends BleEvent {
  const BleSeen(this.device, this.rssi);
  final String device;
  final int rssi;
}

/// A device connected to this one wrote a whole message: the core answers it (ble_server), and
/// the answer goes back by [Ble.reply].
class BleMessage extends BleEvent {
  const BleMessage(this.conn, this.data);
  final String conn;
  final Uint8List data;
}

/// The connection of a device to this one went.
class BleClosed extends BleEvent {
  const BleClosed(this.conn);
  final String conn;
}

/// The radio failed to advertise or to scan, with the system's code.
class BleFailed extends BleEvent {
  const BleFailed(this.what, this.code);
  final String what;
  final int code;
}

/// The Wi-Fi bands this device can create a network on and join one on (bit 0: 2.4 GHz, bit 1:
/// 5 GHz), as the core's ble_server and ble_client take them.
typedef WifiBands = ({int create, int join});

abstract interface class Ble {
  bool get supported;

  /// Whether Bluetooth is on (3 devices of 4 had it off at the probe, WP-D.0).
  Future<bool> enabled();

  /// Asks the permissions BLE needs, at the moment it serves, with their reason (BLE-12).
  Future<bool> permission();

  /// Asks the user to turn Bluetooth on: an app may not on its own. Whether it is on afterwards.
  Future<bool> turnOn();

  /// Advertises the service and answers ([advertise]), scans ([scan]): while the app is open and
  /// the user looks for or waits for a device (D3, DISC-05). The code of a PlatformException
  /// says why not: bluetooth_off, unsupported…
  Future<void> start({bool advertise = true, bool scan = true});
  Future<void> stop();

  Stream<BleEvent> get events;

  /// The answer to the last message of [conn], read by that device.
  Future<void> reply(String conn, Uint8List data);

  /// Writes [message] to [device]'s service and returns its answer; connects first if needed.
  Future<Uint8List> exchange(String device, Uint8List message);
  Future<void> disconnect(String device);

  Future<WifiBands> wifiBands();
}

/// Android: android.bluetooth.le, through the app's Kotlin (Ble.kt).
class AndroidBle implements Ble {
  AndroidBle({this.channel = const MethodChannel('dev.medxfer.app/ble')}) {
    channel.setMethodCallHandler((call) async {
      if (call.method != 'event') return;
      final m = Map<Object?, Object?>.from(call.arguments as Map);
      final e = switch (m['event']) {
        'seen' => BleSeen('${m['device']}', (m['rssi'] as num?)?.toInt() ?? 0),
        'message' => BleMessage('${m['conn']}', m['data'] as Uint8List),
        'closed' => BleClosed('${m['conn']}'),
        'advertise_failed' => BleFailed('advertise', (m['code'] as num?)?.toInt() ?? 0),
        'scan_failed' => BleFailed('scan', (m['code'] as num?)?.toInt() ?? 0),
        _ => null,
      };
      if (e != null) _events.add(e);
    });
  }

  final MethodChannel channel;
  final _events = StreamController<BleEvent>.broadcast();

  @override
  bool get supported => Platform.isAndroid;

  @override
  Future<bool> enabled() async => await channel.invokeMethod<bool>('enabled') ?? false;

  @override
  Future<bool> permission() async => await channel.invokeMethod<bool>('permission') ?? false;

  @override
  Future<bool> turnOn() async => await channel.invokeMethod<bool>('turnOn') ?? false;

  @override
  Future<void> start({bool advertise = true, bool scan = true}) => channel.invokeMethod<void>('start', {'advertise': advertise, 'scan': scan});

  @override
  Future<void> stop() => channel.invokeMethod<void>('stop');

  @override
  Stream<BleEvent> get events => _events.stream;

  @override
  Future<void> reply(String conn, Uint8List data) => channel.invokeMethod<void>('reply', {'conn': conn, 'data': data});

  @override
  Future<Uint8List> exchange(String device, Uint8List message) async =>
      (await channel.invokeMethod<Uint8List>('exchange', {'device': device, 'data': message}))!;

  @override
  Future<void> disconnect(String device) => channel.invokeMethod<void>('disconnect', {'device': device});

  @override
  Future<WifiBands> wifiBands() async {
    final m = await channel.invokeMapMethod<String, Object?>('bands') ?? const {};
    return (create: (m['create'] as num?)?.toInt() ?? 0, join: (m['join'] as num?)?.toInt() ?? 0);
  }
}

/// No BLE here: the QR code and the PIN remain (D4).
class NoBle implements Ble {
  const NoBle();

  @override
  bool get supported => false;

  @override
  Future<bool> enabled() async => false;

  @override
  Future<bool> permission() async => false;

  @override
  Future<bool> turnOn() async => false;

  @override
  Future<void> start({bool advertise = true, bool scan = true}) => throw PlatformException(code: 'unsupported');

  @override
  Future<void> stop() async {}

  @override
  Stream<BleEvent> get events => const Stream.empty();

  @override
  Future<void> reply(String conn, Uint8List data) async {}

  @override
  Future<Uint8List> exchange(String device, Uint8List message) => throw PlatformException(code: 'unsupported');

  @override
  Future<void> disconnect(String device) async {}

  @override
  Future<WifiBands> wifiBands() async => (create: 0, join: 0);
}

Ble systemBle() => Platform.isAndroid ? AndroidBle() : const NoBle();
