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

/// Waits until [done], 2 s at most: what runs on a timer, whatever the machine's load.
Future<void> _until(bool Function() done) async {
  final end = DateTime.now().add(const Duration(seconds: 2));
  while (!done() && DateTime.now().isBefore(end)) {
    await Future<void>.delayed(const Duration(milliseconds: 5));
  }
}

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

  @override
  Stream<void> get changes => const Stream.empty();
}

/// Android's hotspot as the model sees it: what was asked, and what to answer.
class _FakeHotspot implements Hotspot {
  bool granted = true;
  bool joinFails = false;

  /// Wi-Fi off: the join refused (#218), until the panel turns it on if [panelTurnsOn].
  bool wifiOff = false;
  bool panelTurnsOn = true;
  final log = <String>[];

  @override
  bool get canJoin => true;

  @override
  bool get leavesWifi => false;

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
    if (wifiOff) throw PlatformException(code: 'wifi_off', message: 'wifi_off');
    if (joinFails) throw PlatformException(code: 'join', message: 'unavailable');
    return const HotspotInfo(address: '10.46.56.91');
  }

  @override
  Future<void> leave() async => log.add('leave');

  @override
  Future<int> joinedNetwork() async => 643355824141;

  @override
  Future<bool> openWifiPanel() async {
    log.add('wifi panel');
    if (panelTurnsOn) wifiOff = false;
    return !wifiOff;
  }

  @override
  Future<bool> alive() async => true;

  @override
  Future<bool> wifiOn() async => true;
}

/// A desktop's network (WP-C.2 of the J6c plan): it creates one, never joins one itself.
class _DesktopHotspot implements Hotspot {
  final log = <String>[];

  /// The network stopped by the system (#219) when false.
  bool running = true;

  /// Starts that fail before one succeeds.
  int failingStarts = 0;

  /// The computer's Wi-Fi; turned on by [openWifiPanel].
  bool wifi = true;

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
    log.add('start');
    if (failingStarts > 0) {
      failingStarts--;
      throw PlatformException(code: 'hotspot', message: 'the Wi-Fi Direct group was refused (aborted)');
    }
    running = true;
    return const HotspotInfo(
      ssid: 'DIRECT-AB-medXfer',
      passphrase: 'K7XQ2MPRT9ZD',
      security: 1,
      address: '192.168.137.1',
      net: NetInterface(name: 'Connexion au réseau local* 2', index: 23, addrs: ['192.168.137.1/24']),
    );
  }

  @override
  Future<void> stop() async => log.add('stop');

  @override
  Future<HotspotInfo> join(String ssid, String passphrase, {int security = 1}) async {
    log.add('join');
    return const HotspotInfo();
  }

  @override
  Future<void> leave() async => log.add('leave');

  @override
  Future<int> joinedNetwork() async => 0;

  @override
  Future<bool> openWifiPanel() async {
    log.add('wifi on');
    return wifi = true;
  }

  @override
  Future<bool> alive() async => running;

  @override
  Future<bool> wifiOn() async => wifi;
}

List<Object?> _networkNames(FakeCore core) => [
      for (final c in core.calls)
        if (c.action == 'set_network_info') [for (final i in c.payload['interfaces']! as List) (i as Map)['name']],
    ];

void main() {
  test('opening Connect announces this device and looks around; the code is drawn on demand', () async {
    final core = _core();
    final c = Connection(core);
    await c.openConnect();
    // No QR code of the shared network: one scanned elsewhere would fail.
    expect(core.calls.map((x) => x.action), ['set_discoverable', 'scan']);
    expect(core.calls.first.payload, {'on': true});
    expect(c.invitation, isNull);
    // The access point's screen, or a device missing from the list, asks for it.
    await c.openCode();
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
    await c.openCode();
    core.calls.clear();
    await c.setRemember(false);
    expect(core.calls.map((x) => x.action), ['pair_show', 'pair_pin_show']);
    expect(core.calls.every((x) => x.payload.isEmpty), isTrue);
    await c.pairUri('  medxfer://v1/pair?fp=AAAA  ');
    expect(core.calls.last.payload, {'uri': 'medxfer://v1/pair?fp=AAAA'});
  });

  test('real test of 1 November: one attempt at a time, and a device that does not answer is said so', () async {
    final core = _core()
      ..on('scan', (_) => const FakeAnswer(replies: [
            CoreEvent('peers_list', data: [
              {'device_name': 'medXfer-2926', 'host_ip': '192.168.42.172', 'port': 18887},
            ]),
          ]))
      ..on('pair', (_) => const FakeAnswer()); // the core answers later, once its dial gave up
    final c = Connection(core);
    final notices = <ConnectionNotice>[];
    c.notices.listen(notices.add);
    await c.scan();
    final d = c.nearby.single;
    await c.pairWith(d);
    final id = 'ui-${core.calls.length}';
    expect(c.connecting, '192.168.42.172');
    await c.pairWith(d); // tapped again meanwhile: no second attempt
    expect(core.calls.where((x) => x.action == 'pair'), hasLength(1));

    c.take(CoreEvent('action_error', id: id, data: {
      'error': 'failed to pair with 192.168.42.172:18887: dial tcp4 192.168.42.172:18887: i/o timeout',
      'code': 'unreachable',
      'target': '192.168.42.172:18887',
    }));
    await _settle();
    expect(c.connecting, isNull);
    expect(notices.single, isA<PeerUnreachable>().having((n) => n.name, 'name', 'medXfer-2926'));
    await c.pairWith(d); // a new attempt is possible again
    expect(core.calls.where((x) => x.action == 'pair'), hasLength(2));
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

  test("J6c: the session coming back is kept, whatever status says in the core's grace period", () async {
    final c = Connection(FakeCore());
    c.take(const CoreEvent('paired', data: {'ip': '169.254.121.148', 'device_name': 'PC'}));
    c.take(const CoreEvent('reconnecting', data: {'message': '…'}));
    // set_network_info, when the network comes back, answers a status: not paired yet.
    c.take(const CoreEvent('status', data: {'paired': false}));
    expect(c.session?.name, 'PC', reason: 'dropped, the foreground service went, and Android cut the network');
    c.take(const CoreEvent('paired', data: {'ip': '169.254.121.148', 'device_name': 'PC'}));
    expect(c.reconnecting, isFalse);
    // Outside a reconnection, status still says the truth.
    c.take(const CoreEvent('status', data: {'paired': false}));
    expect(c.session, isNull);
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
    // The access point's screen draws the code once the access point exists (HotspotScreen).
    await c.openCode();
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

  test('#218: Wi-Fi off, said as such; turned on in the panel, the same link joined without a new scan', () async {
    final core = _core()
      ..on('set_network_info', (_) => const FakeAnswer())
      ..on('pair_uri', (_) => const FakeAnswer());
    final h = _FakeHotspot()..wifiOff = true;
    final c = Connection(core, hotspot: h);
    final notices = <ConnectionNotice>[];
    c.notices.listen(notices.add);
    final link = PairLink.parse('medxfer://v1/pair?fp=A&ws=Net&wp=x')!;
    await c.pairLink(link);
    await _settle();
    expect(notices.single, isA<WifiOff>().having((n) => n.link, 'link', link));
    expect(core.calls.where((x) => x.action == 'pair_uri'), isEmpty);

    await c.joinWithWifiOn(link);
    await _settle();
    expect(h.log, ['permission', 'join Net x 1', 'wifi panel', 'permission', 'join Net x 1']);
    expect(core.calls.last.action, 'pair_uri');
  });

  test('#218: the panel closed with Wi-Fi still off: said again, nothing joined', () async {
    final core = _core()..on('pair_uri', (_) => const FakeAnswer());
    final h = _FakeHotspot()
      ..wifiOff = true
      ..panelTurnsOn = false;
    final c = Connection(core, hotspot: h);
    final notices = <ConnectionNotice>[];
    c.notices.listen(notices.add);
    final link = PairLink.parse('medxfer://v1/pair?fp=A&ws=Net&wp=x')!;
    await c.joinWithWifiOn(link);
    await _settle();
    expect(notices.single, isA<WifiOff>());
    expect(h.log, ['wifi panel']);
    expect(core.calls.where((x) => x.action == 'pair_uri'), isEmpty);
  });

  test("WP-C.2: a desktop's own network: its interface alone for the core, then the enumeration back", () async {
    final core = _core()..on('set_network_info', (_) => const FakeAnswer());
    final h = _DesktopHotspot();
    final c = Connection(core, hotspot: h); // a desktop: the core enumerates by itself
    await c.openConnect();
    expect(core.calls.where((x) => x.action == 'set_network_info'), isEmpty);
    await c.startHotspot();
    expect(core.calls.where((x) => x.action == 'set_network_info').single.payload, {
      'interfaces': [
        {'name': 'Connexion au réseau local* 2', 'index': 23, 'addrs': ['192.168.137.1/24']},
      ],
    });
    core.calls.clear();
    await c.stopHotspot();
    expect(h.log.last, 'stop');
    expect(core.calls.where((x) => x.action == 'set_network_info').single.payload, {'enumerate': true});
  });

  test('#219: the network stopped by the system, its QR code on screen: a new one, no message', () async {
    final old = Connection.hotspotCheck;
    Connection.hotspotCheck = const Duration(milliseconds: 20);
    addTearDown(() => Connection.hotspotCheck = old);
    final core = _core()..on('set_network_info', (_) => const FakeAnswer());
    final h = _DesktopHotspot();
    final c = Connection(core, hotspot: h);
    addTearDown(c.dispose);
    final notices = <ConnectionNotice>[];
    c.notices.listen(notices.add);
    await c.openCode();
    await c.startHotspot();
    await Future<void>.delayed(const Duration(milliseconds: 60));
    expect(h.log, ['start']); // still running: nothing done

    h.running = false; // the computer's Wi-Fi joined another network
    await _until(() => h.log.length >= 3 && c.myHotspot != null);
    expect(h.log.take(3), ['start', 'stop', 'start']);
    expect(c.myHotspot, isNotNull);
    expect(notices, isEmpty); // the screen shows what goes on: no passing message
  });

  test('#219: the new network fails, tried again quietly until it runs', () async {
    final old = Connection.hotspotCheck;
    Connection.hotspotCheck = const Duration(milliseconds: 20);
    addTearDown(() => Connection.hotspotCheck = old);
    final core = _core()..on('set_network_info', (_) => const FakeAnswer());
    final h = _DesktopHotspot();
    final c = Connection(core, hotspot: h);
    addTearDown(c.dispose);
    final notices = <ConnectionNotice>[];
    c.notices.listen(notices.add);
    await c.openCode();
    await c.startHotspot();
    h
      ..failingStarts = 3
      ..running = false;
    await _until(() => c.myHotspot != null && h.log.length >= 6);
    expect(h.log, ['start', 'stop', 'start', 'start', 'start', 'start']);
    expect(notices, isEmpty);
  });

  test('#219: its Wi-Fi turned off: said on its screen, no try; turned on, the network comes back', () async {
    final old = Connection.hotspotCheck;
    Connection.hotspotCheck = const Duration(milliseconds: 20);
    addTearDown(() => Connection.hotspotCheck = old);
    final core = _core()..on('set_network_info', (_) => const FakeAnswer());
    final h = _DesktopHotspot();
    final c = Connection(core, hotspot: h);
    addTearDown(c.dispose);
    final notices = <ConnectionNotice>[];
    c.notices.listen(notices.add);
    await c.openCode();
    await c.startHotspot();
    h
      ..wifi = false
      ..running = false;
    await _until(() => c.hotspotWifiOff);
    await Future<void>.delayed(const Duration(milliseconds: 60));
    expect(h.log, ['start', 'stop']); // no try while it is off
    expect(c.myHotspot, isNull);

    await c.turnWifiOn();
    await _until(() => c.myHotspot != null);
    expect(h.log, ['start', 'stop', 'wifi on', 'start']);
    expect(c.hotspotWifiOff, isFalse);
    expect(notices, isEmpty);
  });

  test('#219: created with its Wi-Fi off: no error, it waits for the Wi-Fi', () async {
    final old = Connection.hotspotCheck;
    Connection.hotspotCheck = const Duration(milliseconds: 20);
    addTearDown(() => Connection.hotspotCheck = old);
    final core = _core()..on('set_network_info', (_) => const FakeAnswer());
    final h = _DesktopHotspot()..wifi = false;
    final c = Connection(core, hotspot: h);
    addTearDown(c.dispose);
    final notices = <ConnectionNotice>[];
    c.notices.listen(notices.add);
    await c.startHotspot();
    expect((c.myHotspot, c.hotspotWifiOff), (null, true));
    expect(h.log, isEmpty);
    h.wifi = true; // turned on in the system's settings
    await _until(() => c.myHotspot != null);
    expect(h.log, ['start']);
    expect(notices, isEmpty);

    await c.stopHotspot();
    expect(c.hotspotWifiOff, isFalse);
  });

  test('#219: the network stopped during a session: said, none created behind it', () async {
    final old = Connection.hotspotCheck;
    Connection.hotspotCheck = const Duration(milliseconds: 20);
    addTearDown(() => Connection.hotspotCheck = old);
    final core = _core()..on('set_network_info', (_) => const FakeAnswer());
    final h = _DesktopHotspot();
    final c = Connection(core, hotspot: h);
    addTearDown(c.dispose);
    final notices = <ConnectionNotice>[];
    c.notices.listen(notices.add);
    await c.openCode();
    await c.startHotspot();
    c.take(const CoreEvent('paired', data: {'ip': '192.168.137.20', 'device_name': 'Galaxy A57'}));
    h.running = false;
    await _until(() => notices.isNotEmpty && c.myHotspot == null);
    await Future<void>.delayed(const Duration(milliseconds: 60)); // no new one behind it
    expect(notices.whereType<HotspotLost>(), hasLength(1));
    expect(h.log, ['start', 'stop']);
    expect(c.myHotspot, isNull);
  });

  test('WP-C.2: a desktop joins no network itself: the link pairs, the user joined it first', () async {
    final core = _core()..on('pair_uri', (_) => const FakeAnswer());
    final h = _DesktopHotspot();
    final c = Connection(core, hotspot: h);
    await c.pairLink(PairLink.parse('medxfer://v1/pair?fp=AAAA&a=10.46.56.232&p=18887&s=x&exp=$_expires&ws=Net&wp=secret12&wt=1')!);
    expect(h.log, isEmpty);
    expect(core.calls.where((x) => x.action == 'pair_uri'), hasLength(1));
  });

  test("J6c: a network joined by a QR code: its handle goes to the core, whose sockets go through it", () async {
    final core = _core()
      ..on('set_network_info', (_) => const FakeAnswer())
      ..on('pair_uri', (_) => const FakeAnswer());
    final h = _FakeHotspot();
    final c = Connection(core, network: _AndroidNetwork(), hotspot: h);
    await c.pairLink(PairLink.parse('medxfer://v1/pair?fp=AAAA&a=169.254.121.148&p=18887&s=x&exp=$_expires&ws=DIRECT-VF-medXfer&wp=K7XQ2MPRT9ZD&wt=1')!);
    final joined = core.calls.where((x) => x.action == 'set_network_info').last.payload;
    expect(joined['network'], 643355824141);
    expect(core.calls.last.action, 'pair_uri', reason: 'paired once the network is given');
    await c.leaveJoined();
    expect(core.calls.where((x) => x.action == 'set_network_info').last.payload.containsKey('network'), isFalse);
  });

  test("J6c: a session timed out keeps the network joined by a QR code, joined again; a disconnect leaves it", () async {
    final core = _core()
      ..on('set_network_info', (_) => const FakeAnswer())
      ..on('pair_uri', (_) => const FakeAnswer());
    final h = _FakeHotspot();
    final c = Connection(core, network: _AndroidNetwork(), hotspot: h);
    final link = PairLink.parse('medxfer://v1/pair?fp=AAAA&a=169.254.121.148&p=18887&s=x&exp=$_expires&ws=DIRECT-ND-medXfer&wp=K7XQ2MPRT9ZD&wt=1')!;
    await c.pairLink(link);
    c.take(const CoreEvent('paired', data: {'ip': '169.254.121.148', 'device_name': 'PC'}));
    c.take(const CoreEvent('reconnecting', data: {}));
    c.take(const CoreEvent('disconnected', data: {'reason': 'connection timed out'}));
    await _settle();
    expect(h.log, isNot(contains('leave')), reason: 'leaving gave up the rejoin under way, in the background');
    expect(c.joining, isTrue);
    // Back: the session too.
    c.take(const CoreEvent('paired', data: {'ip': '169.254.121.148', 'device_name': 'PC'}));
    // A disconnect asked for leaves it.
    c.take(const CoreEvent('disconnected', data: {'reason': 'user requested disconnect'}));
    await _settle();
    expect(h.log.last, 'leave');
    expect(c.joining, isFalse);
  });

  test('J6c: a network joined that does not come back is left after rejoinFor', () async {
    final old = Connection.rejoinFor;
    Connection.rejoinFor = const Duration(milliseconds: 20);
    addTearDown(() => Connection.rejoinFor = old);
    final core = _core()
      ..on('set_network_info', (_) => const FakeAnswer())
      ..on('pair_uri', (_) => const FakeAnswer());
    final h = _FakeHotspot();
    final c = Connection(core, network: _AndroidNetwork(), hotspot: h);
    await c.pairLink(PairLink.parse('medxfer://v1/pair?fp=AAAA&a=169.254.121.148&p=18887&s=x&exp=$_expires&ws=DIRECT-ND-medXfer&wp=K7XQ2MPRT9ZD&wt=1')!);
    c.take(const CoreEvent('paired', data: {'ip': '169.254.121.148', 'device_name': 'PC'}));
    c.take(const CoreEvent('disconnected', data: {'reason': 'connection timed out'}));
    await Future<void>.delayed(const Duration(milliseconds: 60));
    expect(h.log.last, 'leave');
    expect(c.joining, isFalse);
  });
}
