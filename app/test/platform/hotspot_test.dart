import 'dart:io';
import 'dart:math';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/platform/hotspot.dart';

/// nmcli as Zorin answers it (probe WP-C.1), recording each call.
class _Nmcli {
  final calls = <String>[];
  bool hotspotFails = false;

  /// The hotspot connection active: up once started, until something else takes the device.
  bool up = false;

  Future<ProcessResult> call(String exe, List<String> args) async {
    final line = '$exe ${args.join(' ')}';
    calls.add(line);
    var out = '';
    var code = 0;
    if (line.contains('-f DEVICE,TYPE device')) out = 'enp0s25:ethernet\nwlo1:wifi\nlo:loopback\n';
    if (line.contains('connection show --active')) out = 'TECNO POP 7:wlo1\nlo:lo\n';
    if (line.contains('device wifi hotspot')) {
      if (hotspotFails) {
        code = 4;
      } else {
        up = true;
      }
    }
    if (line.contains('-f NAME connection show --active')) out = up ? 'medxfer-hotspot\nlo\n' : 'TECNO POP 7\nlo\n';
    if (line.contains('IP4.ADDRESS')) out = '10.42.0.1/24\n';
    return ProcessResult(0, code, out, code == 0 ? '' : 'Error: hotspot network creation took too long');
  }
}

void main() {
  test('WP-C.2: a desktop network says nothing of the device, a passphrase drawn each time', () {
    final a = drawNetwork(Random(1));
    final b = drawNetwork(Random(2));
    expect(a.ssid, matches(RegExp(r'^DIRECT-[A-Z]{2}-medXfer$')));
    expect(a.passphrase, hasLength(12));
    expect(a.passphrase, isNot(b.passphrase));
  });

  test('WP-C.2: Linux, NetworkManager at 2.4 GHz; the network it was on comes back at the stop', () async {
    final nm = _Nmcli();
    final h = LinuxHotspot(run: nm.call);
    expect((h.supported, h.canJoin, h.leavesWifi), (true, false, true));
    final info = await h.start();
    expect(info.address, '10.42.0.1');
    expect(info.security, 1);
    expect(info.net!.name, 'wlo1');
    expect(info.net!.addrs, ['10.42.0.1/24']);
    final up = nm.calls.singleWhere((c) => c.contains('device wifi hotspot'));
    expect(up, contains('ifname wlo1 con-name medxfer-hotspot ssid ${info.ssid} band bg password ${info.passphrase}'));
    nm.calls.clear();
    await h.stop();
    expect(nm.calls, [
      'nmcli connection down medxfer-hotspot',
      'nmcli connection delete medxfer-hotspot',
      'nmcli connection up TECNO POP 7',
    ]);
  });

  test('#219: Linux, the network is alive while its connection is active', () async {
    final nm = _Nmcli();
    final h = LinuxHotspot(run: nm.call);
    expect(await h.alive(), isFalse);
    await h.start();
    expect(await h.alive(), isTrue);
    nm.up = false; // the user picked another Wi-Fi: NetworkManager ended the hotspot
    expect(await h.alive(), isFalse);
  });

  test('WP-C.2: Linux, a network that does not come up says why, and leaves nothing', () async {
    final nm = _Nmcli()..hotspotFails = true;
    final h = LinuxHotspot(run: nm.call);
    await expectLater(h.start(), throwsA(isA<PlatformException>().having((e) => e.message, 'message', contains('took too long'))));
    expect(nm.calls.last, 'nmcli connection up TECNO POP 7');
    expect(nm.calls, contains('nmcli connection delete medxfer-hotspot'));
  });

  group('WP-C.5: Windows', () {
    TestWidgetsFlutterBinding.ensureInitialized();
    const channel = MethodChannel('dev.medxfer.app/hotspot');
    final calls = <String>[];
    late Map<String, Object?> group;
    var alive = false;
    setUp(() {
      calls.clear();
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger.setMockMethodCallHandler(channel, (call) async {
        calls.add(call.method);
        return switch (call.method) { 'start' => group, 'alive' => alive, _ => null };
      });
    });
    tearDown(() => TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger.setMockMethodCallHandler(channel, null));

    test('Windows serves the addresses: no server of ours, a stable network', () async {
      group = {'name': 'Connexion au réseau local* 2', 'index': 25, 'address': '192.168.137.1', 'prefix': 24, 'windowsDhcp': true, 'heldBy': '', 'sharingAddress': ''};
      final served = <String>[];
      final h = WindowsHotspot(addressServer: (server, prefix) async {
        served.add('${server.address}/$prefix');
        return () {};
      });
      final info = await h.start();
      expect(served, isEmpty);
      expect((info.address, info.dropsClients, info.addressHeldBy), ('192.168.137.1', false, null));
      expect(info.net!.addrs, ['192.168.137.1/24']);
    });

    test('#219: alive as the runner says it', () async {
      final h = WindowsHotspot(addressServer: (server, prefix) async => () {});
      alive = true;
      expect(await h.alive(), isTrue);
      alive = false;
      expect(await h.alive(), isFalse);
      expect(calls, ['alive', 'alive']);
    });

    test('its sharing taken by another card: our server, and the card named', () async {
      group = {'name': 'Connexion au réseau local* 2', 'index': 25, 'address': '169.254.121.148', 'prefix': 16, 'windowsDhcp': false, 'heldBy': 'Ethernet', 'sharingAddress': '192.168.137.1'};
      final served = <String>[];
      final h = WindowsHotspot(addressServer: (server, prefix) async {
        served.add('${server.address}/$prefix');
        return () => served.add('closed');
      });
      final info = await h.start();
      expect(served, ['169.254.121.148/16']);
      expect((info.dropsClients, info.addressHeldBy, info.sharingAddress), (true, 'Ethernet', '192.168.137.1'));
      await h.stop();
      expect(served.last, 'closed');
      expect(calls, ['start', 'stop']);
    });
  });

  test('HotspotInfo reads what LocalHotspot.kt sends', () {
    final info = HotspotInfo.fromMap({
      'ssid': 'AndroidShare_1732',
      'passphrase': 'secret',
      'security': 2,
      'address': '10.46.56.232',
      'interfaces': ['wlan0=192.168.1.20', 'swlan0=10.46.56.232'],
    });
    expect(info.ssid, 'AndroidShare_1732');
    expect(info.passphrase, 'secret');
    expect(info.security, 2);
    expect(info.address, '10.46.56.232');
    expect(info.interfaces, ['wlan0=192.168.1.20', 'swlan0=10.46.56.232']);
  });

  test('HotspotInfo of a join: an address, no network secrets', () {
    final info = HotspotInfo.fromMap({'address': '10.46.56.91', 'interfaces': null});
    expect(info.ssid, isNull);
    expect(info.passphrase, isNull);
    expect(info.security, 0);
    expect(info.interfaces, isEmpty);
  });
}
