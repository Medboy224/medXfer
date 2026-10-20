// The connection model on the fake core: what it asks the core, and what it makes of the
// events (WP-B.2).
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/connection/pair_link.dart';
import 'package:medxfer/platform/hotspot.dart';
import 'package:medxfer/platform/network.dart';
import 'package:medxfer/connection/connection.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';

const _expires = 1900000000; // 2030: never past in these tests

FakeCore _core() => FakeCore()
  ..on('set_discoverable', (_) => const FakeAnswer())
  ..on('pair_show', (p) => const FakeAnswer(replies: [
        CoreEvent('pair_show', data: {'uri': 'medxfer://v1/pair?fp=AAAA&a=192.168.1.20&p=18887&s=x&exp=$_expires', 'expires': _expires, 'instance': 'medXfer-7810'}),
      ]))
  ..on('pair_pin_show', (_) => const FakeAnswer(replies: [
        CoreEvent('pair_pin_shown', data: {'pin': '054 461', 'expires': _expires, 'address': '192.168.1.20:18887', 'instance': 'medXfer-7810'}),
      ]))
  ..on('scan', (_) => const FakeAnswer(replies: [
        CoreEvent('peers_list', data: [
          {'device_name': 'medXfer-1A2B', 'host_ip': '192.168.1.30', 'port': 18887},
          {'device_name': 'medXfer-9F00', 'host_ip': '192.168.1.31', 'port': 18887, 'trusted_alias': 'Bureau'},
        ]),
      ]));

Future<void> _settle() => Future<void>.delayed(Duration.zero);

/// Android as the model sees it: interfaces to give, a multicast lock.
class _AndroidNetwork implements NetworkInfo {
  final locks = <bool>[];

  @override
  Future<List<NetInterface>?> interfaces() async => const [
        NetInterface(name: 'wlan0', index: 30, addrs: ['192.168.119.123/24']),
        NetInterface(name: 'swlan0', addrs: ['10.46.56.232/24']),
      ];

  @override
  Future<void> multicast(bool on) async => locks.add(on);
}

/// Android's hotspot as the model sees it: what was asked, and what to answer.
class _FakeHotspot implements Hotspot {
  bool granted = true;
  bool joinFails = false;
  final log = <String>[];

  @override
  bool get supported => true;

  @override
  Future<bool> permission() async {
    log.add('permission');
    return granted;
  }

  @override
  Future<HotspotInfo> start() async {
    log.add('start');
    return const HotspotInfo(ssid: 'AndroidShare_1732', passphrase: 'vvrtsy', security: 2, address: '10.46.56.232');
  }

  @override
  Future<void> stop() async => log.add('stop');

  @override
  Future<HotspotInfo> join(String ssid, String passphrase, {int security = 1}) async {
    log.add('join $ssid $passphrase $security');
    if (joinFails) throw PlatformException(code: 'join', message: 'unavailable');
    return const HotspotInfo(address: '10.46.56.91');
  }

  @override
  Future<void> leave() async => log.add('leave');
}

List<Object?> _networkNames(FakeCore core) => [
      for (final c in core.calls)
        if (c.action == 'set_network_info') [for (final i in c.payload['interfaces']! as List) (i as Map)['name']],
    ];

void main() {
  test('opening Connect announces this device, shows its code and looks around', () async {
    final core = _core();
    final c = Connection(core);
    await c.openConnect();
    expect(core.calls.map((x) => x.action), ['set_discoverable', 'pair_show', 'scan', 'pair_pin_show']);
    expect(core.calls.first.payload, {'on': true});
    expect(core.calls.firstWhere((x) => x.action == 'pair_show').payload, {'trust': true});
    expect(c.invitation!.instance, 'medXfer-7810');
    expect(c.invitation!.expires, DateTime.fromMillisecondsSinceEpoch(_expires * 1000));
    expect(c.pin!.pin, '054 461');
    // Trusted first, under the name recorded here; the others under their generic name.
    expect(c.nearby.map((d) => (d.name, d.trusted, d.target)), [('Bureau', true, '192.168.1.31:18887'), ('medXfer-1A2B', false, '192.168.1.30:18887')]);
    expect(c.scanning, isFalse);

    await c.closeConnect();
    expect(core.calls.last.action, 'set_discoverable');
    expect(core.calls.last.payload, {'on': false});
    expect(c.invitation, isNull);
    expect(c.pin, isNull);
  });

  test('without "remember", the code and the pairings carry no trust offer', () async {
    final core = _core()..on('pair_uri', (_) => const FakeAnswer());
    final c = Connection(core);
    await c.openConnect();
    core.calls.clear();
    await c.setRemember(false);
    expect(core.calls.map((x) => x.action), ['pair_show', 'pair_pin_show']);
    expect(core.calls.every((x) => x.payload.isEmpty), isTrue);
    await c.pairUri('  medxfer://v1/pair?fp=AAAA  ');
    expect(core.calls.last.payload, {'uri': 'medxfer://v1/pair?fp=AAAA'});
  });

  test('a device of the list asks for its PIN, which the user types', () async {
    final core = _core()
      ..on('pair', (_) => const FakeAnswer(broadcast: [CoreEvent('pin_needed', data: {'target': '192.168.1.30:18887'})]))
      ..on('pair_pin', (_) => const FakeAnswer(broadcast: [
            CoreEvent('paired', data: {'ip': '192.168.1.30', 'device_name': 'Pixel 8', 'fingerprint': 'BDIT-4HBW', 'mutual': false}),
          ]));
    final c = Connection(core);
    final notices = <ConnectionNotice>[];
    c.notices.listen(notices.add);
    await c.pairWith(const NearbyDevice(name: 'medXfer-1A2B', ip: '192.168.1.30', port: 18887));
    expect(core.calls.last.payload, {'ip': '192.168.1.30', 'port': 18887, 'trust': true});
    await _settle();
    expect(c.pinNeeded, '192.168.1.30:18887');

    await c.pairPin(c.pinNeeded!, '054461');
    expect(core.calls.last.action, 'pair_pin');
    expect(core.calls.last.payload, {'target': '192.168.1.30:18887', 'pin': '054461', 'trust': true});
    expect(c.pinNeeded, isNull);
    await _settle();
    expect(c.session!.name, 'Pixel 8');
    expect(c.session!.fingerprint, 'BDIT-4HBW');
    expect(notices.single, isA<Paired>());

    // The second paired (pair_accept) brings the name only: the key stays.
    c.take(const CoreEvent('paired', data: {'ip': '192.168.1.30', 'device_name': 'Pixel 8 Pro'}));
    expect(c.session!.name, 'Pixel 8 Pro');
    expect(c.session!.fingerprint, 'BDIT-4HBW');
    expect(notices, hasLength(1));
  });

  test('a disconnection says who left', () async {
    final c = Connection(FakeCore());
    final notices = <ConnectionNotice>[];
    c.notices.listen(notices.add);
    c.take(const CoreEvent('paired', data: {'ip': '10.0.0.2', 'device_name': 'PC'}));
    c.take(const CoreEvent('reconnecting', data: {'message': '…'}));
    expect(c.reconnecting, isTrue);
    c.take(const CoreEvent('disconnected', data: {'reason': 'peer requested disconnect'}));
    expect(c.session, isNull);
    expect(c.reconnecting, isFalse);
    c.take(const CoreEvent('paired', data: {'ip': '10.0.0.2', 'device_name': 'PC'}));
    c.take(const CoreEvent('disconnected', data: {'reason': 'user requested disconnect'}));
    // Without a session, a disconnection says nothing.
    c.take(const CoreEvent('disconnected', data: {'reason': 'connection timed out'}));
    await _settle();
    expect(notices.whereType<Disconnected>().map((d) => d.byPeer), [true, false]);
  });

  test('the status gives the session after a restart of the interface', () {
    final c = Connection(FakeCore());
    c.take(const CoreEvent('status', data: {'paired': true, 'paired_device': 'PC', 'paired_ip': '10.0.0.2'}));
    expect(c.session!.name, 'PC');
    c.take(const CoreEvent('status', data: {'paired': false}));
    expect(c.session, isNull);
  });

  test('mutual trust: offered, answered, established', () async {
    final core = FakeCore()
      ..on('trust_answer', (_) => const FakeAnswer())
      ..on('trust_list', (_) => const FakeAnswer(replies: [
            CoreEvent('trusted_devices', data: {
              'devices': [
                {'fingerprint': 'BDIT4HBWIMJS', 'short': 'BDIT-4HBW', 'alias': 'Pixel 8', 'last_address': '192.168.1.30'},
              ],
            }),
          ]));
    final c = Connection(core);
    final notices = <ConnectionNotice>[];
    c.notices.listen(notices.add);
    c.take(const CoreEvent('paired', data: {'ip': '192.168.1.30', 'device_name': 'Pixel 8', 'mutual': false}));
    c.take(const CoreEvent('trust_offered', data: {'device_name': 'Pixel 8', 'fingerprint': 'BDIT-4HBW', 'timeout_s': 60}));
    expect(c.trustOffer!.fingerprint, 'BDIT-4HBW');
    expect(c.trustOffer!.deadline.isAfter(DateTime.now().add(const Duration(seconds: 50))), isTrue);
    await c.answerTrust(true);
    expect(core.calls.last.payload, {'accept': true});
    expect(c.trustOffer, isNull);

    c.take(const CoreEvent('trust_established', data: {'device_name': 'Pixel 8', 'fingerprint': 'BDIT-4HBW'}));
    expect(c.session!.mutual, isTrue);
    await _settle();
    expect(core.calls.last.action, 'trust_list');
    expect(c.trusted.single.alias, 'Pixel 8');
    expect(c.trusted.single.lastAddress, '192.168.1.30');

    c.take(const CoreEvent('trust_declined', data: {'reason': 'no_answer'}));
    c.take(const CoreEvent('trust_incomplete', data: {'device_name': 'Bureau', 'side': 'local'}));
    await _settle();
    expect(notices.whereType<TrustEstablished>().single.name, 'Pixel 8');
    expect(notices.whereType<TrustDeclined>().single.noAnswer, isTrue);
    expect(notices.whereType<TrustIncomplete>().single.local, isTrue);
  });

  test('a known device with another key: refused, and said (ID-05)', () async {
    final core = FakeCore()
      ..on('pair', (_) => const FakeAnswer(
            ok: false,
            error: 'failed',
            replies: [CoreEvent('action_error', data: {'error': "failed to pair with 192.168.1.30:18887: REFUSED: 192.168.1.30 is the address of 'Bureau'"})],
          ));
    final c = Connection(core);
    final notices = <ConnectionNotice>[];
    c.notices.listen(notices.add);
    await c.pairWith(const NearbyDevice(name: 'Bureau', ip: '192.168.1.30', port: 18887));
    // Late, and before the id was known: still shown.
    c.take(const CoreEvent('action_error', id: 'elsewhere', data: {'error': 'REFUSED: again'}));
    // Someone else's error: not this model's to show.
    c.take(const CoreEvent('action_error', id: 'elsewhere', data: {'error': 'disk full'}));
    await _settle();
    expect(notices.map((n) => n is KeyChanged ? n.message : '$n'), ["192.168.1.30 is the address of 'Bureau'", 'again']);
  });

  test('a PIN asked for by another device, then read out (PAIR-11)', () {
    final c = Connection(FakeCore());
    c.take(const CoreEvent('pin_requested', data: {'pin': '123 456', 'expires': _expires, 'from': '192.168.1.40', 'instance': 'medXfer-7810'}));
    expect(c.askedPin!.pin, '123 456');
    expect(c.askedPin!.from, '192.168.1.40');
    c.dismissAskedPin();
    expect(c.askedPin, isNull);
    c.take(const CoreEvent('pin_requested', data: {'pin': '', 'stopped': true}));
    expect(c.askedPin!.stopped, isTrue);
  });

  test('the trusted devices: rename and forget by fingerprint', () async {
    final core = FakeCore()
      ..on('trust_rename', (_) => const FakeAnswer())
      ..on('trust_forget', (_) => const FakeAnswer());
    final c = Connection(core);
    const d = TrustedDevice(fingerprint: 'BDIT4HBWIMJS', short: 'BDIT-4HBW', alias: 'Pixel 8');
    await c.rename(d, ' Téléphone de Awa ');
    expect(core.calls.last.payload, {'fingerprint': 'BDIT4HBWIMJS', 'name': 'Téléphone de Awa'});
    await c.forget(d);
    expect(core.calls.last.payload, {'fingerprint': 'BDIT4HBWIMJS'});
  });

  test('Android: the core gets the interfaces, and multicast is let in while Connect is open', () async {
    final core = _core()..on('set_network_info', (_) => const FakeAnswer());
    final net = _AndroidNetwork();
    final c = Connection(core, network: net);
    await c.openConnect();
    expect(core.calls.first.action, 'set_network_info');
    expect(core.calls.first.payload, {
      'interfaces': [
        {'name': 'wlan0', 'index': 30, 'addrs': ['192.168.119.123/24']},
        {'name': 'swlan0', 'addrs': ['10.46.56.232/24']},
      ],
    });
    expect(net.locks, [true]);
    await c.closeConnect();
    expect(net.locks, [true, false]);
  });

  testWidgets('a scan that never answers stops showing as running', (tester) async {
    final core = FakeCore()..on('scan', (_) => const FakeAnswer());
    final c = Connection(core);
    await c.scan();
    expect(c.scanning, isTrue);
    await tester.pump(Connection.scanLimit);
    expect(c.scanning, isFalse);
    c.dispose();
  });

  test("this device's access point: only its interface for the core, the network in the QR code", () async {
    final core = _core()
      ..on('set_network_info', (_) => const FakeAnswer());
    final h = _FakeHotspot();
    final c = Connection(core, network: _AndroidNetwork(), hotspot: h);
    await c.openConnect();
    core.calls.clear();
    await c.startHotspot();
    expect(h.log, ['permission', 'start']);
    expect(c.myHotspot!.ssid, 'AndroidShare_1732');
    expect(_networkNames(core), [['swlan0']]);
    expect(core.calls.map((x) => x.action), ['set_network_info', 'pair_show', 'pair_pin_show']);
    final link = PairLink.parse(c.qrData!)!;
    expect(link.network!.ssid, 'AndroidShare_1732');
    expect(link.network!.passphrase, 'vvrtsy');
    expect(link.network!.security, 2);

    // Nobody joined: closing Connect stops it, and the core gets every interface back.
    await c.closeConnect();
    expect(h.log.last, 'stop');
    expect(c.myHotspot, isNull);
    expect(_networkNames(core).last, ['wlan0', 'swlan0']);
  });

  test('an access point that carries the session stays until the session ends', () async {
    final core = _core()..on('set_network_info', (_) => const FakeAnswer());
    final h = _FakeHotspot();
    final c = Connection(core, network: _AndroidNetwork(), hotspot: h);
    await c.openConnect();
    await c.startHotspot();
    c.take(const CoreEvent('paired', data: {'ip': '10.46.56.91', 'device_name': 'PC'}));
    await c.closeConnect();
    expect(h.log, isNot(contains('stop')));
    c.take(const CoreEvent('disconnected', data: {'reason': 'peer requested disconnect'}));
    await _settle();
    await _settle();
    expect(h.log.last, 'stop');
  });

  test('no permission, no access point: said, nothing started', () async {
    final h = _FakeHotspot()..granted = false;
    final c = Connection(_core(), hotspot: h);
    final notices = <ConnectionNotice>[];
    c.notices.listen(notices.add);
    await c.startHotspot();
    await _settle();
    expect(h.log, ['permission']);
    expect(c.myHotspot, isNull);
    expect((notices.single as HotspotFailed).permission, isTrue);
  });

  test('a link with a network: joined first, then paired; left when the session ends', () async {
    final core = _core()
      ..on('set_network_info', (_) => const FakeAnswer())
      ..on('pair_uri', (_) => const FakeAnswer());
    final h = _FakeHotspot();
    final c = Connection(core, network: _AndroidNetwork(), hotspot: h);
    final uri = PairLink.withNetwork('medxfer://v1/pair?fp=A&a=10.46.56.232&p=18887&s=x&exp=1900000000',
        const LinkNetwork(ssid: 'AndroidShare_1732', passphrase: 'vvrtsy', security: 2));
    await c.pairLink(PairLink.parse(uri)!);
    expect(h.log, ['permission', 'join AndroidShare_1732 vvrtsy 2']);
    expect(core.calls.map((x) => x.action), ['set_network_info', 'pair_uri']);
    expect(core.calls.last.payload['uri'], uri);
    c.take(const CoreEvent('paired', data: {'ip': '10.46.56.232', 'device_name': 'Galaxy'}));
    c.take(const CoreEvent('disconnected', data: {'reason': 'user requested disconnect'}));
    await _settle();
    expect(h.log.last, 'leave');
  });

  test('a network that cannot be joined: said, and no pairing tried', () async {
    final core = _core()..on('pair_uri', (_) => const FakeAnswer());
    final h = _FakeHotspot()..joinFails = true;
    final c = Connection(core, hotspot: h);
    final notices = <ConnectionNotice>[];
    c.notices.listen(notices.add);
    await c.pairLink(PairLink.parse('medxfer://v1/pair?fp=A&ws=Net&wp=x')!);
    await _settle();
    expect((notices.single as JoinFailed).ssid, 'Net');
    expect(core.calls.where((x) => x.action == 'pair_uri'), isEmpty);
  });
}
