// The path by BLE between the radio and the core (WP-D.3): the radio and the core faked, the
// steps the link runs and what it does with their answers checked.
import 'dart:async';
import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/connection/ble_link.dart';
import 'package:medxfer/connection/connection.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';
import 'package:medxfer/platform/ble.dart';
import 'package:medxfer/platform/hotspot.dart';

/// The radio: what it was asked to send, and the answers it gets.
class _Radio implements Ble {
  final _events = StreamController<BleEvent>.broadcast();
  final replies = <String, Uint8List>{};
  final disconnected = <String>[];
  final hang = <String>{}; // devices that never answer: out of reach, or an old address
  bool on = true, allowed = true, started = false;
  int asked = 0; // the permission's system dialog
  WifiBands bands = (create: 3, join: 3);

  void add(BleEvent e) => _events.add(e);

  @override
  bool get supported => true;
  @override
  Future<bool> enabled() async => on;
  @override
  Future<bool> permission() async {
    asked++;
    return allowed;
  }

  @override
  Future<bool> granted() async => allowed;
  @override
  Future<bool> turnOn() async => on = true;
  @override
  Future<void> start({bool advertise = true, bool scan = true}) async => started = true;
  @override
  Future<void> stop() async => started = false;
  @override
  Stream<BleEvent> get events => _events.stream;
  @override
  Future<void> reply(String conn, Uint8List data) async => replies[conn] = data;
  @override
  Future<Uint8List> exchange(String device, Uint8List message) =>
      hang.contains(device) ? Completer<Uint8List>().future : Future.value(Uint8List.fromList([...message, 0]));
  @override
  Future<void> disconnect(String device) async => disconnected.add(device);
  @override
  Future<WifiBands> wifiBands() async => bands;
}

/// The phone's networks: the one it creates, the ones it joins.
class _Hotspot implements Hotspot {
  final starts = <bool>[];
  final joins = <String>[];

  @override
  bool get supported => true;
  @override
  bool get canJoin => true;
  @override
  bool get leavesWifi => false;
  @override
  Future<bool> permission() async => true;
  @override
  Future<HotspotInfo> start({bool fiveGhz = true}) async {
    starts.add(fiveGhz);
    return fiveGhz
        ? const HotspotInfo(ssid: 'DIRECT-KQ-medXfer', passphrase: 'K3PQ8Z2MXA7D', security: 1, address: '192.168.49.1', band: WifiBand.ghz5)
        : const HotspotInfo(ssid: 'AndroidShare_1853', passphrase: 'vvrtsy12', security: 2, address: '10.34.109.23');
  }

  @override
  Future<void> stop() async {}
  @override
  Future<HotspotInfo> join(String ssid, String passphrase, {int security = 1, WifiBand? band}) async {
    joins.add('$ssid $band');
    return const HotspotInfo(address: '192.168.49.20');
  }

  @override
  Future<void> leave() async {}
  @override
  Future<int> joinedNetwork() async => 0;
  @override
  Future<bool> openWifiPanel({bool always = false}) async => true;
  @override
  Future<bool> otherWifi() async => false;
  @override
  Future<bool> alive() async => true;
  @override
  Future<bool> wifiOn() async => true;
}

Future<void> _settle() async {
  for (var i = 0; i < 30; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

FakeAnswer _reply(String action, Map<String, Object?> data) => FakeAnswer(replies: [CoreEvent(action, data: data)]);

final _data = base64Encode([1, 2, 3]);

/// The steps of the core's ble_client a test scripts, by name; the others answer with data.
void _client(FakeCore core, Map<String, Map<String, Object?>> steps) =>
    core.on('ble_client', (p) => _reply('ble_client', steps[p['step']] ?? {'data': _data}));

void main() {
  late FakeCore core;
  late _Radio radio;
  late _Hotspot hotspot;
  late Connection connection;
  late BleLink link;
  setUpAll(() => BleLink.recognizeJitter = Duration.zero);
  setUp(() {
    core = FakeCore()..on('set_network_info', (_) => const FakeAnswer());
    radio = _Radio();
    hotspot = _Hotspot();
    connection = Connection(core, hotspot: hotspot);
    link = BleLink(core, radio, connection);
  });
  tearDown(() async {
    await connection.stopHotspot();
    link.dispose();
    connection.dispose();
  });

  Iterable<Map<String, Object?>> steps(String action) => core.calls.where((c) => c.action == action).map((c) => c.payload);

  test('BLE-12: no permission, Bluetooth off: said; turned on, it runs', () async {
    radio.allowed = false;
    await link.start();
    expect((link.running, link.problem), (false, 'permission'));
    radio
      ..allowed = true
      ..on = false;
    await link.start();
    expect((link.running, link.problem), (false, 'bluetooth_off'));
    await link.turnOn();
    expect((link.running, link.problem, radio.started), (true, null, true));
  });

  test('D3: the connection screen opens: started without the permission dialog; it closes: stopped', () async {
    radio.allowed = false;
    await connection.openConnect();
    await _settle();
    expect((link.running, link.problem, radio.asked), (false, 'permission', 0)); // the screen gives the reason first
    radio.allowed = true;
    await link.start(); // the user's "Allow"
    expect((link.running, radio.asked), (true, 1));
    await connection.closeConnect();
    await _settle();
    expect((link.running, radio.started), (false, false));
    await connection.openConnect();
    await _settle();
    expect((link.running, radio.asked), (true, 1));
  });

  test('D3: paused while a session is open, started again after it', () async {
    await connection.openConnect();
    await _settle();
    expect(radio.started, isTrue);
    connection.take(const CoreEvent('paired', data: {'ip': '10.34.109.23', 'device_name': 'TECNO'}));
    await _settle();
    expect((radio.started, link.running, link.paused), (false, false, true));
    connection.take(const CoreEvent('disconnected', data: {'reason': 'peer requested disconnect'}));
    await _settle();
    expect((radio.started, link.running, link.paused), (true, true, false));
  });

  test('touched by an unknown device: the core answers each message, the phone creates its network at 5 GHz, the answer is read', () async {
    core.on('ble_server', (p) => switch (p['step']) {
          'message' => _reply('ble_server', {
              'conn': 'aa',
              'request': {'fingerprint': 'FPB', 'trusted': false, 'create': 1, 'join': 3, 'outcome': 1, 'band': 2},
            }),
          'answer' => _reply('ble_server', {'conn': 'aa', 'data': base64Encode([7, 7])}),
          _ => const FakeAnswer(),
        });
    await link.start();
    radio.add(BleMessage('aa', Uint8List.fromList([1, 2])));
    await _settle();
    final message = steps('ble_server').first;
    expect((message['data'], message['create'], message['join']), (base64Encode([1, 2]), 3, 3));
    expect(hotspot.starts, [true]);
    final answer = steps('ble_server').last;
    expect(answer['outcome'], 1);
    expect(answer['network'], {'ssid': 'DIRECT-KQ-medXfer', 'passphrase': 'K3PQ8Z2MXA7D', 'security': 1, 'band': 2, 'addrs': ['192.168.49.1']});
    expect(radio.replies['aa'], [7, 7]);
  });

  test('devices seen: each recognized once; the same device under a new address replaces the old', () async {
    _client(core, {
      'recognize': {'suffix': '6e2a', 'trusted': false},
    });
    await link.start();
    radio.add(const BleSeen('11:11', -60));
    await _settle();
    expect(link.devices.map((d) => (d.name, d.trusted)), [('medXfer-6e2a', false)]);
    radio.add(const BleSeen('22:22', -50));
    await _settle();
    expect(link.devices.map((d) => d.device), ['22:22']);
    expect(radio.disconnected, ['11:11', '22:22', '11:11']); // each recognition closes its connection, the old address too
  });

  test('a device of mutual trust: its network joined at 5 GHz, then the session by P3', () async {
    _client(core, {
      'recognize': {'suffix': 'ab12', 'trusted': true, 'fingerprint': 'FPA', 'alias': 'Phone'},
      'reply': {
        'fingerprint': 'FPA', 'outcome': 1, 'band': 2,
        'network': {'ssid': 'DIRECT-KQ-medXfer', 'passphrase': 'K3PQ8Z2MXA7D', 'security': 1, 'band': 2, 'port': 18887, 'addrs': ['192.168.49.1']},
      },
    });
    core.on('pair', (_) => const FakeAnswer());
    await link.start();
    radio.add(const BleSeen('33:33', -40));
    await _settle();
    final phone = link.devices.single;
    expect((phone.name, phone.trusted), ('Phone', true));
    await link.connect(phone);
    expect(steps('ble_client').map((p) => p['step']), containsAllInOrder(['hello', 'recognize', 'trusted', 'reply']));
    expect(hotspot.joins, ['DIRECT-KQ-medXfer WifiBand.ghz5']);
    expect(steps('pair').single, {'ip': '192.168.49.1', 'port': 18887});
    expect(link.stage, BleStage.idle);
    // WP-D.6: each step's time, from the touch to the session.
    connection.take(const CoreEvent('paired', data: {'ip': '192.168.49.1', 'device_name': 'Phone'}));
    await _settle();
    expect(link.lastTimings.split(', ').map((m) => m.substring(0, m.lastIndexOf(' '))), ['touched', 'asking', 'confirming', 'connecting', 'joined ghz5', 'idle', 'session']);
  });

  test('an unknown device: its PIN typed; a wrong one is refused, said', () async {
    _client(core, {
      'recognize': {'suffix': '6e2a', 'trusted': false},
      'pin_ready': {'ready': true},
      'pin_reply': {'refused': true},
    });
    await link.start();
    radio.add(const BleSeen('11:11', -60));
    await _settle();
    await link.connect(link.devices.single);
    expect(link.stage, BleStage.pinNeeded);
    await link.typePin('123 456');
    expect(steps('ble_client').firstWhere((p) => p['step'] == 'pin')['pin'], '123 456');
    expect((link.stage, link.failure), (BleStage.failed, 'refused'));
  });

  test('an unknown device that cannot create a network: this phone creates it, at the band given, and gives it', () async {
    _client(core, {
      'recognize': {'suffix': '6e2a', 'trusted': false},
      'pin_ready': {'ready': true},
      'reply': {'fingerprint': 'FPB', 'outcome': 2, 'band': 1},
      'ack': {'done': true},
    });
    await link.start();
    radio.add(const BleSeen('11:11', -60));
    await _settle();
    await link.connect(link.devices.single);
    await link.typePin('123456');
    expect(hotspot.starts, [false]); // at 2.4 GHz: the other does not join 5 GHz
    final network = steps('ble_client').firstWhere((p) => p['step'] == 'network')['network'];
    expect(network, {'ssid': 'AndroidShare_1853', 'passphrase': 'vvrtsy12', 'security': 2, 'band': 1, 'addrs': ['10.34.109.23']});
    expect(link.stage, BleStage.idle); // the other joins it, then connects (pair_ble)
  });

  test('a device the radio stops reporting leaves the list; one recognition at a time', () async {
    final old = BleLink.forgetAfter;
    BleLink.forgetAfter = const Duration(milliseconds: 50);
    addTearDown(() => BleLink.forgetAfter = old);
    _client(core, {
      'recognize': {'suffix': '6e2a', 'trusted': false},
    });
    await link.start();
    radio
      ..add(const BleSeen('11:11', -60))
      ..add(const BleSeen('22:22', -60)); // the same moment: waits for the first
    await _settle();
    // One after the other, never two at once; the same device under its new address replaces
    // the old one.
    expect(steps('ble_client').map((p) => '${p['step']} ${p['conn']}'), ['hello 11:11', 'recognize 11:11', 'hello 22:22', 'recognize 22:22']);
    expect(link.devices.map((d) => d.device), ['22:22']);
    await Future<void>.delayed(const Duration(milliseconds: 1200)); // the check runs every second
    expect(link.devices, isEmpty);
    expect(radio.disconnected, containsAll(['11:11', '22:22']));
  });

  test('DISC-05: stopped, this device answers nothing, the platform keeping its service; the last reports are ignored', () async {
    core.on('ble_server', (_) => _reply('ble_server', {'conn': 'aa', 'data': _data}));
    radio
      ..add(BleMessage('aa', Uint8List.fromList([1])))
      ..add(const BleSeen('11:11', -60)); // delivered after the stop
    await _settle();
    expect(link.devices, isEmpty);
    expect(steps('ble_client'), isEmpty);
    expect(steps('ble_server'), isEmpty);
    expect(radio.replies, isEmpty);
  });

  test('a device being recognized by another waits servedQuiet before recognizing it in turn', () async {
    final quiet = BleLink.servedQuiet;
    BleLink.servedQuiet = const Duration(milliseconds: 80);
    addTearDown(() => BleLink.servedQuiet = quiet);
    core.on('ble_server', (_) => _reply('ble_server', {'conn': 'aa', 'data': _data}));
    _client(core, {
      'recognize': {'suffix': '6e2a', 'trusted': false},
    });
    await link.start();
    radio.add(BleMessage('aa', Uint8List.fromList([1]))); // the other recognizes this one
    await _settle();
    radio.add(const BleSeen('11:11', -60));
    await _settle();
    expect(steps('ble_client'), isEmpty);
    await Future<void>.delayed(const Duration(milliseconds: 120));
    await _settle();
    expect(link.devices.map((d) => d.name), ['medXfer-6e2a']);
  });

  test('a recognition with no answer is given up: the next device has its turn, the first waits retryAfter', () async {
    final within = BleLink.recognizeWithin;
    BleLink.recognizeWithin = const Duration(milliseconds: 50);
    addTearDown(() => BleLink.recognizeWithin = within);
    _client(core, {
      'recognize': {'suffix': '6e2a', 'trusted': false},
    });
    await link.start();
    radio.hang.add('old');
    radio.add(const BleSeen('old', -70)); // a phone's old address, no longer advertised
    await _settle();
    radio.add(const BleSeen('new', -50));
    await Future<void>.delayed(const Duration(milliseconds: 120));
    await _settle();
    expect(link.devices.map((d) => d.device), ['new']);
    expect(radio.disconnected, contains('old'));
    radio.add(const BleSeen('old', -70)); // seen again at once: not tried before retryAfter
    await _settle();
    expect(steps('ble_client').where((p) => p['step'] == 'hello' && p['conn'] == 'old'), hasLength(1));
  });

  test('BLE-09: a network created for BLE that nobody joins is stopped after bleUnjoinedFor', () async {
    final old = Connection.bleUnjoinedFor;
    Connection.bleUnjoinedFor = const Duration(milliseconds: 60);
    addTearDown(() => Connection.bleUnjoinedFor = old);
    final net = await connection.networkForBle(fiveGhz: true);
    expect((net?.ssid, connection.myHotspot?.ssid), ('DIRECT-KQ-medXfer', 'DIRECT-KQ-medXfer'));
    await Future<void>.delayed(const Duration(milliseconds: 120));
    expect(connection.myHotspot, isNull);
    // Joined in time: kept.
    await connection.networkForBle(fiveGhz: true);
    connection.take(const CoreEvent('paired', data: {'ip': '192.168.49.20', 'device_name': 'PC'}));
    await Future<void>.delayed(const Duration(milliseconds: 120));
    expect(connection.myHotspot?.ssid, 'DIRECT-KQ-medXfer');
  });

  test('the radio fails during an exchange: said, the exchange may start again', () async {
    _client(core, {
      'recognize': {'suffix': '6e2a', 'trusted': false},
    });
    await link.start();
    radio.add(const BleSeen('11:11', -60));
    await _settle();
    final failing = _FailingRadio(radio);
    final l2 = BleLink(core, failing, connection);
    addTearDown(l2.dispose);
    await l2.connect(link.devices.single);
    expect((l2.stage, l2.failure), (BleStage.failed, 'timeout'));
  });
}

/// A radio whose exchanges fail as Android's do when the other goes out of reach.
class _FailingRadio extends _Radio {
  _FailingRadio(_Radio _);

  @override
  Future<Uint8List> exchange(String device, Uint8List message) => throw PlatformException(code: 'ble', message: 'timeout');
}
