/// The local access point (DISC-12, WP-B.7 of the J6b plan): the platform creates it, never
/// the core. Android only for now; elsewhere [Hotspot.supported] is false.
library;

import 'dart:io';

import 'folders.dart' show platformChannel;

/// A network created, or joined, by the app.
class HotspotInfo {
  const HotspotInfo({this.ssid, this.passphrase, this.security = 0, this.address, this.interfaces = const []});

  factory HotspotInfo.fromMap(Map<Object?, Object?> m) => HotspotInfo(
        ssid: m['ssid'] as String?,
        passphrase: m['passphrase'] as String?,
        security: (m['security'] as num?)?.toInt() ?? 0,
        address: m['address'] as String?,
        interfaces: [for (final i in (m['interfaces'] as List?) ?? const []) '$i'],
      );

  /// Name and passphrase, chosen by Android: secrets of the network, never logged.
  final String? ssid;
  final String? passphrase;

  /// 1: WPA2-PSK, 2: WPA3 transition, 3: WPA3-SAE (SoftApConfiguration).
  final int security;

  /// This device's address on that network.
  final String? address;
  final List<String> interfaces;
}

abstract interface class Hotspot {
  bool get supported;

  /// Creates the access point: its name, passphrase and this device's address on it.
  Future<HotspotInfo> start();
  Future<void> stop();

  /// Joins another device's access point (the QR gives ssid and passphrase): the app's
  /// connections, the core's included, then go through it.
  Future<HotspotInfo> join(String ssid, String passphrase, {int security = 1});
  Future<void> leave();
}

/// Android's local hotspot, through the app's Kotlin (LocalHotspot.kt, WifiJoin.kt).
class AndroidHotspot implements Hotspot {
  const AndroidHotspot();

  @override
  bool get supported => Platform.isAndroid;

  @override
  Future<HotspotInfo> start() async =>
      HotspotInfo.fromMap((await platformChannel.invokeMethod<Map<Object?, Object?>>('hotspotStart'))!);

  @override
  Future<void> stop() => platformChannel.invokeMethod<void>('hotspotStop');

  @override
  Future<HotspotInfo> join(String ssid, String passphrase, {int security = 1}) async => HotspotInfo.fromMap(
      (await platformChannel.invokeMethod<Map<Object?, Object?>>('hotspotJoin', {'ssid': ssid, 'passphrase': passphrase, 'security': security}))!);

  @override
  Future<void> leave() => platformChannel.invokeMethod<void>('hotspotLeave');
}
