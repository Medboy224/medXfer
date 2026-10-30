/// The local access point (DISC-12, WP-B.7 of the J6b plan): the platform creates it, never
/// the core. Android: the local hotspot. Windows and Linux (WP-C.2 of the J6c plan): a network
/// the computer creates at 2.4 GHz, its cards refusing 5 GHz on their own (probe WP-C.1);
/// Windows by Wi-Fi Direct with a name and passphrase of the app's (decision C4), Linux by
/// NetworkManager. A computer does not join another's network itself: the user does, in the
/// system's settings ([Hotspot.canJoin]).
library;

import 'dart:io';
import 'dart:math';

import 'package:flutter/services.dart';

import 'dhcp_server.dart';
import 'folders.dart' show platformChannel;
import 'network.dart';

/// A network created, or joined, by the app.
class HotspotInfo {
  const HotspotInfo({
    this.ssid,
    this.passphrase,
    this.security = 0,
    this.address,
    this.interfaces = const [],
    this.net,
    this.dropsClients = false,
    this.addressHeldBy,
    this.sharingAddress,
  });

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

  /// A desktop: the interface the core uses while the network runs (DISC-10), its QR code,
  /// PIN and announce then carry this address. Android gives its own interfaces.
  final NetInterface? net;

  /// Windows serves no addresses on this network: it drops each phone after 2 minutes, the
  /// phone comes back by itself (WP-C.5 of the J6c plan). The card that holds its connection
  /// sharing's address, and that address, when one does.
  final bool dropsClients;
  final String? addressHeldBy;
  final String? sharingAddress;
}

/// Starts the address server on the network's address; gives its close. [DhcpServer], or a
/// fake in the tests.
typedef AddressServer = Future<void Function()> Function(InternetAddress server, int prefix);

Future<void Function()> _dhcpServer(InternetAddress server, int prefix) async => (await DhcpServer.start(server, prefix)).close;

abstract interface class Hotspot {
  /// This device creates a network ([start]).
  bool get supported;

  /// This device joins another's network itself ([join]): Android. A desktop's user joins it
  /// in the system's settings.
  bool get canJoin;

  /// The Wi-Fi this device is on is left while the network runs, and comes back at [stop]:
  /// Linux, whose card is one or the other (NetworkManager, without administrator rights).
  /// Android and Windows create theirs on a second, virtual interface.
  bool get leavesWifi;

  /// Asks the permission both start and join need (Android: NEARBY_WIFI_DEVICES); true once
  /// granted.
  Future<bool> permission();

  /// Creates the access point: its name, passphrase and this device's address on it.
  Future<HotspotInfo> start();
  Future<void> stop();

  /// Joins another device's access point (the QR gives ssid and passphrase): the app's
  /// connections, the core's included, then go through it.
  Future<HotspotInfo> join(String ssid, String passphrase, {int security = 1});
  Future<void> leave();

  /// The joined network's handle, for the core's sockets (pkg/netbind); 0 for none. It changes
  /// when the network, lost, is joined again (Android, J6c).
  Future<int> joinedNetwork();

  /// Turns this device's Wi-Fi on, or lets the user do it: Android before [join], by the
  /// system's panel (an app cannot since Android 10, and joins with Wi-Fi off are refused, #218);
  /// a desktop before [start] (#219). Whether Wi-Fi is on afterwards.
  Future<bool> openWifiPanel();

  /// Whether the Wi-Fi [start] needs is on: a desktop's network needs it (#219); Android's local
  /// hotspot starts with Wi-Fi off.
  Future<bool> wifiOn();

  /// Whether the access point [start] created still runs. The system may stop it without a
  /// word to the app: Windows when the computer's Wi-Fi joins another network, Android its local
  /// hotspot (#219). False when none was started.
  Future<bool> alive();
}

/// Android's local hotspot, through the app's Kotlin (LocalHotspot.kt, WifiJoin.kt).
class AndroidHotspot implements Hotspot {
  const AndroidHotspot();

  @override
  bool get supported => Platform.isAndroid;

  @override
  bool get canJoin => Platform.isAndroid;

  @override
  bool get leavesWifi => false;

  @override
  Future<bool> permission() async => await platformChannel.invokeMethod<bool>('hotspotPermission') ?? false;

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

  @override
  Future<int> joinedNetwork() async => await platformChannel.invokeMethod<int>('hotspotJoinedNetwork') ?? 0;

  @override
  Future<bool> openWifiPanel() async => await platformChannel.invokeMethod<bool>('openWifiPanel') ?? false;

  @override
  Future<bool> alive() async => await platformChannel.invokeMethod<bool>('hotspotAlive') ?? false;

  @override
  Future<bool> wifiOn() async => true; // the local hotspot starts with Wi-Fi off
}

/// No hotspot from the app (the user joins a network in the system's settings).
class NoHotspot implements Hotspot {
  const NoHotspot();

  @override
  bool get supported => false;

  @override
  bool get canJoin => false;

  @override
  bool get leavesWifi => false;

  @override
  Future<bool> permission() async => false;

  @override
  Future<HotspotInfo> start() => throw UnsupportedError('no hotspot on this platform');

  @override
  Future<void> stop() async {}

  @override
  Future<HotspotInfo> join(String ssid, String passphrase, {int security = 1}) => throw UnsupportedError('no joining on this platform');

  @override
  Future<void> leave() async {}

  @override
  Future<int> joinedNetwork() async => 0;

  @override
  Future<bool> openWifiPanel() async => false;

  @override
  Future<bool> alive() async => false;

  @override
  Future<bool> wifiOn() async => true;
}

/// What a desktop's network looks like: a name that says nothing of the device (DISC-07), and a
/// passphrase drawn here, for each start.
({String ssid, String passphrase}) drawNetwork([Random? random]) {
  final r = random ?? Random.secure();
  const letters = 'ABCDEFGHJKLMNPQRSTUVWXYZ';
  const all = '${letters}23456789';
  String draw(int n, String from) => String.fromCharCodes([for (var i = 0; i < n; i++) from.codeUnitAt(r.nextInt(from.length))]);
  return (ssid: 'DIRECT-${draw(2, letters)}-medXfer', passphrase: draw(12, all));
}

/// Windows: a Wi-Fi Direct group this computer owns, joined as an ordinary Wi-Fi network
/// (WiFiDirectAdvertisementPublisher, "legacy" settings), through the runner's C++/WinRT
/// (hotspot.cpp). No Internet connection to share, no administrator rights (probe WP-C.1).
/// Windows serves the addresses on it, through its connection sharing, and accepts the phones.
/// When that sharing is taken by another card, it serves none: [DhcpServer] does, on the group's
/// address only, and Windows drops each phone after 2 minutes ([HotspotInfo.dropsClients]).
class WindowsHotspot implements Hotspot {
  WindowsHotspot({this.channel = const MethodChannel('dev.medxfer.app/hotspot'), AddressServer? addressServer})
      : _addressServer = addressServer ?? _dhcpServer;

  final MethodChannel channel;
  final AddressServer _addressServer;
  void Function()? _closeDhcp;

  @override
  bool get supported => true;

  @override
  bool get canJoin => false;

  @override
  bool get leavesWifi => false;

  @override
  Future<bool> permission() async => true;

  @override
  Future<HotspotInfo> start() async {
    final net = drawNetwork();
    final group = (await channel.invokeMapMethod<String, Object?>('start', {'ssid': net.ssid, 'passphrase': net.passphrase}))!;
    final address = '${group['address']}';
    final prefix = (group['prefix'] as num?)?.toInt() ?? 16;
    // Two servers on one network would answer each other's clients: ours only when Windows
    // serves none.
    final windowsDhcp = group['windowsDhcp'] == true;
    if (!windowsDhcp) {
      try {
        _closeDhcp = await _addressServer(InternetAddress(address), prefix);
      } on SocketException catch (e) {
        await stop();
        throw PlatformException(code: 'hotspot', message: 'no address server: ${e.message}');
      }
    }
    String? text(String key) => switch (group[key]) { final String s when s.isNotEmpty => s, _ => null };
    return HotspotInfo(
      ssid: net.ssid,
      passphrase: net.passphrase,
      security: 1,
      address: address,
      net: NetInterface(name: '${group['name']}', index: (group['index'] as num?)?.toInt() ?? 0, addrs: ['$address/$prefix']),
      dropsClients: !windowsDhcp,
      addressHeldBy: text('heldBy'),
      sharingAddress: text('sharingAddress'),
    );
  }

  @override
  Future<void> stop() async {
    _closeDhcp?.call();
    _closeDhcp = null;
    await channel.invokeMethod<void>('stop');
  }

  @override
  Future<HotspotInfo> join(String ssid, String passphrase, {int security = 1}) => throw UnsupportedError('a computer joins in the system settings');

  @override
  Future<void> leave() async {}

  @override
  Future<int> joinedNetwork() async => 0;

  /// Turned on through Windows' radios, when the user lets apps do it; else its settings open.
  @override
  Future<bool> openWifiPanel() async => await channel.invokeMethod<bool>('turnWifiOn') ?? false;

  @override
  Future<bool> alive() async => await channel.invokeMethod<bool>('alive') ?? false;

  @override
  Future<bool> wifiOn() async => await channel.invokeMethod<bool>('wifiOn') ?? true;
}

/// Runs a program: Process.run, or a fake in the tests.
typedef Runner = Future<ProcessResult> Function(String executable, List<String> arguments);

/// Linux: NetworkManager's hotspot on the Wi-Fi device (nmcli), 2.4 GHz. The network it was on
/// comes back at the stop. The passphrase goes through nmcli's arguments, seen by the local
/// users for an instant: a passphrase of this network only, drawn at each start.
class LinuxHotspot implements Hotspot {
  LinuxHotspot({Runner? run}) : _run = run ?? Process.run;

  final Runner _run;
  static const connection = 'medxfer-hotspot';
  String? _previous;

  @override
  bool get supported => true;

  @override
  bool get canJoin => false;

  @override
  bool get leavesWifi => true;

  @override
  Future<bool> permission() async => true;

  Future<ProcessResult> _nmcli(List<String> args) => _run('nmcli', args);

  @override
  Future<HotspotInfo> start() async {
    final devices = await _nmcli(['-t', '-f', 'DEVICE,TYPE', 'device']);
    final device = [
      for (final l in '${devices.stdout}'.split('\n'))
        if (l.endsWith(':wifi')) l.substring(0, l.length - ':wifi'.length),
    ].firstOrNull;
    if (devices.exitCode != 0 || device == null) throw PlatformException(code: 'hotspot', message: 'no Wi-Fi device');
    final active = await _nmcli(['-t', '-f', 'NAME,DEVICE', 'connection', 'show', '--active']);
    _previous = [
      for (final l in '${active.stdout}'.split('\n'))
        if (l.endsWith(':$device')) l.substring(0, l.length - ':$device'.length).replaceAll('\\:', ':'),
    ].firstOrNull;
    await _nmcli(['connection', 'delete', connection]); // left by an earlier run, if any
    final net = drawNetwork();
    final up = await _nmcli([
      'device', 'wifi', 'hotspot', 'ifname', device, 'con-name', connection, //
      'ssid', net.ssid, 'band', 'bg', 'password', net.passphrase,
    ]);
    if (up.exitCode != 0) {
      await stop();
      throw PlatformException(code: 'hotspot', message: '${up.stderr}'.trim());
    }
    String? cidr;
    for (var i = 0; i < 20 && (cidr == null || cidr.isEmpty); i++) {
      final ip = await _nmcli(['-g', 'IP4.ADDRESS', 'connection', 'show', connection]);
      cidr = '${ip.stdout}'.split('\n').first.split('|').first.trim();
      if (cidr.isEmpty) await Future<void>.delayed(const Duration(milliseconds: 500));
    }
    if (cidr == null || !cidr.contains('/')) {
      await stop();
      throw PlatformException(code: 'hotspot', message: 'the network has no address');
    }
    final index = (await NetworkInterface.list(includeLinkLocal: true)).where((i) => i.name == device).map((i) => i.index).firstOrNull ?? 0;
    return HotspotInfo(
      ssid: net.ssid,
      passphrase: net.passphrase,
      security: 1,
      address: cidr.split('/').first,
      net: NetInterface(name: device, index: index, addrs: [cidr]),
    );
  }

  @override
  Future<void> stop() async {
    await _nmcli(['connection', 'down', connection]);
    await _nmcli(['connection', 'delete', connection]);
    final previous = _previous;
    _previous = null;
    if (previous != null) await _nmcli(['connection', 'up', previous]);
  }

  @override
  Future<HotspotInfo> join(String ssid, String passphrase, {int security = 1}) => throw UnsupportedError('joining is the system settings\' job');

  @override
  Future<void> leave() async {}

  @override
  Future<int> joinedNetwork() async => 0;

  @override
  Future<bool> openWifiPanel() async => (await _nmcli(['radio', 'wifi', 'on'])).exitCode == 0;

  /// The connection still active: NetworkManager gives the Wi-Fi device back to another network
  /// the user picks, ending it.
  @override
  Future<bool> alive() async {
    final active = await _nmcli(['-t', '-f', 'NAME', 'connection', 'show', '--active']);
    return '${active.stdout}'.split('\n').contains(connection);
  }

  @override
  Future<bool> wifiOn() async => '${(await _nmcli(['radio', 'wifi'])).stdout}'.trim() != 'disabled';
}

Hotspot systemHotspot() {
  if (Platform.isAndroid) return const AndroidHotspot();
  if (Platform.isWindows) return WindowsHotspot();
  if (Platform.isLinux) return LinuxHotspot();
  return const NoHotspot();
}
