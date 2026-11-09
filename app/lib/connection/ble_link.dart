/// The path by BLE between the radio ([Ble]) and the core (bible §6.6, WP-D.3 of the J6d plan).
///
/// Touched by another device, this one answers by itself: the core reads each message
/// (ble_server); once the other is authenticated, by the pair's key or by the PIN this screen
/// shows (pin_requested, as over the network), the device that can create the network creates it
/// and gives it, encrypted; the other joins it, then opens the session on it (pair_ble, or P3
/// between devices of mutual trust).
///
/// Its user touching another device, it runs the same exchange from the other side (ble_client):
/// [connect], then [typePin] for an unknown device.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

import '../core/core.dart';
import '../platform/ble.dart';
import '../platform/hotspot.dart';
import 'connection.dart';
import 'pair_link.dart';

/// A device seen by BLE, and what its recognition told (BLE-04).
class BleDevice {
  BleDevice(this.device);

  /// Its BLE address, which the system changes now and then.
  final String device;
  int rssi = 0;

  /// When the radio last said it advertises: a device silent for [BleLink.forgetAfter] is gone.
  DateTime seenAt = DateTime.now();

  /// A recognition failed: not tried again before then, for the others to have their turn.
  DateTime retryAt = DateTime(0);

  /// Its recognitions failed in a row.
  int failures = 0;

  /// Its generic name's 4 hex digits (DISC-07): the same name as on the network.
  String? suffix;

  /// When of mutual trust: the name this device gave it, and its key.
  String? alias;
  String? fingerprint;

  bool get recognized => suffix != null;
  bool get trusted => fingerprint != null;

  /// At the edge of reach ([BleLink.weakRssi]).
  bool get weak => rssi != 0 && rssi <= BleLink.weakRssi;
  String get name => alias ?? 'medXfer-$suffix';
}

/// Where the exchange this device started stands.
enum BleStage { idle, asking, pinNeeded, confirming, networking, connecting, waiting, failed }

/// Bands as the core takes them: bit 0 2.4 GHz, bit 1 5 GHz.
const _band5 = 2;

/// An exchange the radio dropped: out of reach, or the link lost.
class _RadioLost implements Exception {
  const _RadioLost(this.why);
  final String why;
}

class BleLink extends ChangeNotifier {
  BleLink(this.core, this.ble, this.connection) {
    _events = ble.events.listen(_take);
    connection.addListener(_follow);
  }

  final Core core;
  final Ble ble;
  final Connection connection;
  late final StreamSubscription<BleEvent> _events;

  bool _running = false;
  String? _problem;
  final _devices = <String, BleDevice>{};
  final _recognizing = <String>{};
  final _random = Random();
  Timer? _forget;

  /// A device the radio no longer reports (each one advertising is reported every 2 s, Ble.kt
  /// and ble.cpp) leaves the list after this long: three reports missed.
  static Duration forgetAfter = const Duration(seconds: 6);

  /// A signal this weak is at the edge of reach: through two walls, -82 to -96 dBm, the radio
  /// reported the other one time in several, and a touch worked at the third (real test of
  /// 8 November). Such a device is kept [forgetWeakAfter] rather than [forgetAfter], for the
  /// list not to blink.
  static const weakRssi = -80;
  static Duration forgetWeakAfter = const Duration(seconds: 15);

  /// A touch the radio drops is run again by itself, afresh, this many times in all: the other
  /// forgets the first on its connection's close, and a network already created is given again.
  static int touchTries = 3;
  static Duration touchRetryAfter = const Duration(milliseconds: 600);

  /// A recognition takes 1 to 2 s; one longer is given up, the device tried again later. A
  /// recognition waits for the one before it: an old address of a phone, no longer advertised,
  /// held the computer's list 26 s (real test of 7 November).
  static Duration recognizeWithin = const Duration(seconds: 8);
  static Duration retryAfter = const Duration(seconds: 5);

  /// A recognition the radio dropped (Android's error 133, at the edge of reach) is tried again
  /// sooner: after these, in turn, then [retryAfter]. Through two walls, three tries 5 s apart
  /// kept the computer out of the phone's list 22 s (real test of 8 November). One that got no
  /// answer at all, an old address most often, waits [retryAfter] at once.
  static List<Duration> retrySoonAfter = const [Duration(seconds: 1), Duration(seconds: 2)];
  Timer? _nextTry;

  /// While another device talks to this one (it recognizes it, or touched it), this one does not
  /// start its own recognitions: two at once over one link, one of them hung (7 November).
  static Duration servedQuiet = const Duration(seconds: 3);
  DateTime _servedAt = DateTime(0);
  Timer? _afterQuiet;

  /// This device gave its network: the other joins it, then opens the session, 1 to 3 s after
  /// (real tests of 8 and 9 November). Not open after this long, the touch failed.
  static Duration joinWithin = const Duration(seconds: 30);
  Timer? _joinWait;

  /// An exchange that gets no answer: Android stops its own at 25 s (BLE-11), Windows may not.
  static Duration exchangeWithin = const Duration(seconds: 26);

  /// A recognition starts after a random wait up to this long.
  static Duration recognizeJitter = const Duration(milliseconds: 800);
  final _servedTrusted = <String, bool>{}; // the connections to this device: by the pair's key?

  BleDevice? _target;
  BleStage _stage = BleStage.idle;
  String? _failure;

  bool get supported => ble.supported;

  /// Advertising and scanning.
  bool get running => _running;

  /// Why BLE does not run: unsupported, permission, bluetooth_off, or the system's error.
  String? get problem => _problem;

  /// The devices seen and recognized, of mutual trust first, then the nearest.
  List<BleDevice> get devices => _devices.values.where((d) => d.recognized).toList()..sort((a, b) => a.trusted != b.trusted ? (a.trusted ? -1 : 1) : b.rssi.compareTo(a.rssi));

  BleDevice? get target => _target;
  BleStage get stage => _stage;

  /// Why the exchange failed: refused (a wrong PIN among others, PAIR-07), no_network, no_band, join, or
  /// the radio's error.
  String? get failure => _failure;

  /// Advertises and scans while the user looks for a device or waits for one (D3, DISC-05).
  /// [ask]: the permissions are asked if missing; else their lack is only said ([problem]), for
  /// the screen to give the reason first (BLE-12).
  Future<void> start({bool ask = true}) async {
    if (_running) return;
    _problem = await _cannotStart(ask);
    if (_problem == null) {
      try {
        await ble.start();
        _running = true;
        _forget ??= Timer.periodic(const Duration(seconds: 1), (_) => _forgetSilent());
      } on PlatformException catch (e) {
        _problem = e.code;
      }
    }
    notifyListeners();
  }

  Future<String?> _cannotStart(bool ask) async {
    if (!ble.supported) return 'unsupported';
    if (!await (ask ? ble.permission() : ble.granted())) return 'permission';
    if (!await ble.enabled()) return 'bluetooth_off';
    return null;
  }

  /// Asks the user to turn Bluetooth on (an app may not), then starts.
  Future<void> turnOn() async {
    if (await ble.turnOn()) await start();
  }

  Future<void> stop() async {
    _paused = false;
    if (!_running) return;
    _running = false;
    _forget?.cancel();
    _forget = null;
    _afterQuiet?.cancel();
    _afterQuiet = null;
    _nextTry?.cancel();
    _nextTry = null;
    _devices.clear();
    _servedTrusted.clear();
    await ble.stop();
    notifyListeners();
  }

  bool _paused = false;

  /// Stopped while a session is open, started again after it (D3): BLE only serves to find each
  /// other. Its scan and advertisement at full rate shared the 2.4 GHz radio with the network
  /// just joined, which a phone with one chip for both lost after 70 s (real test of 2 November
  /// 2026).
  bool get paused => _paused;

  bool _showing = false;

  /// Follows the connection: started when its screen opens (the user looks for a device, or
  /// waits for one, D3), stopped when it closes; paused while a session is open.
  void _follow() {
    final inSession = connection.session != null;
    if (inSession) _mark('session');
    if (inSession && _stage == BleStage.waiting) {
      _target = null;
      _set(BleStage.idle);
    }
    if (inSession && _running) {
      unawaited(
        stop().then((_) {
          _paused = true;
          notifyListeners();
        }),
      );
    } else if (!inSession && _paused) {
      _paused = false;
      if (connection.showing) unawaited(start(ask: false));
    }
    if (connection.showing == _showing) return;
    _showing = connection.showing;
    if (_showing && !inSession && supported) {
      unawaited(start(ask: false));
    } else if (!_showing && !_paused) {
      unawaited(stop());
    }
  }

  void _take(BleEvent e) {
    switch (e) {
      case BleSeen(:final device, :final rssi):
        // A report that comes after the stop: the radio delivers its last ones.
        if (!_running) return;
        final d =
            _devices.putIfAbsent(device, () {
                debugPrint('ble: seen $device, $rssi dBm');
                return BleDevice(device);
              })
              ..rssi = rssi
              ..seenAt = DateTime.now();
        if (!d.recognized) _recognizeNext();
        notifyListeners();
      case final BleMessage m:
        // The platform keeps its service between starts: stopped, this device answers nothing
        // (DISC-05), as when it advertised nothing.
        if (!_running) return;
        _servedAt = DateTime.now();
        unawaited(_serve(m));
      case BleClosed(:final conn):
        _servedTrusted.remove(conn);
        unawaited(_step('ble_server', {'conn': conn, 'step': 'close'}));
      case BleFailed(:final what, :final code):
        _problem = '${what}_failed:$code';
        notifyListeners();
    }
  }

  /// One step of ble_server or ble_client: the data of its reply, or null on an error.
  Future<Map<String, Object?>?> _step(String action, Map<String, Object?> payload) async {
    final reply = await core.call(action, payload);
    return reply.first(action)?.map;
  }

  Future<Uint8List?> _send(String device, Object? data, {Duration? within}) async {
    if (data is! String) return null;
    try {
      return await ble
          .exchange(device, base64Decode(data))
          .timeout(
            within ?? exchangeWithin,
            onTimeout: () => throw PlatformException(code: 'ble', message: 'timeout'),
          );
    } on PlatformException catch (e) {
      throw _RadioLost(e.message ?? e.code);
    }
  }

  /// One recognition at a time: a computer may advertise under several addresses in a row, and
  /// two devices recognizing each other at once made one of them fail (real test of 3 November).
  /// The device seen last goes first; one that failed waits [retryAfter].
  void _recognizeNext() {
    if (_recognizing.isNotEmpty) return;
    final now = DateTime.now();
    final quiet = _servedAt.add(servedQuiet);
    if (now.isBefore(quiet)) {
      _afterQuiet?.cancel();
      _afterQuiet = Timer(quiet.difference(now), _recognizeNext);
      return;
    }
    final waiting = [
      for (final d in _devices.values)
        if (!d.recognized && !now.isBefore(d.retryAt)) d,
    ]..sort((a, b) => b.seenAt.compareTo(a.seenAt));
    if (waiting.isEmpty) return _tryLater(now);
    final d = waiting.first;
    _recognizing.add(d.device);
    unawaited(_recognize(d));
  }

  /// A device that failed is tried again at its time, without waiting for the radio's next
  /// report of it: at the edge of reach, one in several comes.
  void _tryLater(DateTime now) {
    final later = [
      for (final d in _devices.values)
        if (!d.recognized) d.retryAt,
    ];
    if (later.isEmpty) return;
    final at = later.reduce((a, b) => a.isBefore(b) ? a : b);
    _nextTry?.cancel();
    _nextTry = Timer(at.difference(now), _recognizeNext);
  }

  /// The devices the radio stopped reporting leave the list: gone, or under a new address.
  void _forgetSilent() {
    final now = DateTime.now();
    final gone = [
      for (final d in _devices.values)
        if (d != _target && now.difference(d.seenAt) > (d.weak ? forgetWeakAfter : forgetAfter)) d,
    ];
    if (gone.isEmpty) return;
    for (final d in gone) {
      debugPrint('ble: ${d.device} gone, last seen ${now.difference(d.seenAt).inMilliseconds} ms ago');
      _devices.remove(d.device);
      unawaited(ble.disconnect(d.device));
    }
    notifyListeners();
  }

  // The server side: another device touched this one.

  Future<void> _serve(BleMessage m) async {
    final own = await ble.wifiBands();
    final r = await _step('ble_server', {'conn': m.conn, 'step': 'message', 'data': base64Encode(m.data), 'create': own.create, 'join': own.join});
    if (r == null) return;
    if (r['data'] case final String data) await ble.reply(m.conn, base64Decode(data));
    if (r['request'] case final Map<String, Object?> request) {
      // The other's user touched this device: its request authenticated, by the pair's key or
      // the PIN typed. The recognition before it says nothing of the touch.
      _clock = null;
      _startClock('request${request['trusted'] == true ? '' : ' after the PIN'}');
      await _answer(m.conn, request, own);
    }
    if (r['network'] case final Map<String, Object?> network) {
      // The other created its network: join it, then connect (BLE-10).
      unawaited(_joinAndConnect(network, '${r['fingerprint']}', trusted: _servedTrusted[m.conn] ?? false));
    }
  }

  /// The answer to a request authenticated: this device's network when it is the one to create it
  /// (Creator, bible §6.6), at the band agreed.
  Future<void> _answer(String conn, Map<String, Object?> request, WifiBands own) async {
    _servedTrusted[conn] = request['trusted'] == true;
    var outcome = (request['outcome'] as num?)?.toInt() ?? 3;
    Map<String, Object?>? network;
    if (outcome == 1) {
      final net = await connection.networkForBle(fiveGhz: ((request['band'] as num?)?.toInt() ?? 0) == _band5);
      _mark(net == null ? 'no network' : 'network ${net.band?.name ?? 'created'}');
      if (net?.ssid == null || net?.address == null) {
        outcome = 3; // none could be created: said, rather than a network that is not there
      } else {
        network = _networkOf(net!);
      }
    }
    final r = await _step('ble_server', {'conn': conn, 'step': 'answer', 'outcome': outcome, 'network': ?network});
    if (r?['data'] case final String data) await ble.reply(conn, base64Decode(data));
  }

  /// A network of this device as the core takes it: the core adds its own port.
  Map<String, Object?> _networkOf(HotspotInfo net) => {
    'ssid': net.ssid,
    'passphrase': net.passphrase,
    'security': net.security == 0 ? 1 : net.security,
    'band': net.band == WifiBand.ghz5 ? 2 : 1,
    'addrs': [net.address],
  };

  // The client side: this device's user touched another.

  /// Recognizes a device newly seen: its generic name, and whether it is of mutual trust. The
  /// same device under a new address replaces the old one.
  Future<void> _recognize(BleDevice d) async {
    final clock = Stopwatch()..start();
    String? failed;
    try {
      // Not at the same moment as the other device, which may be recognizing this one.
      final jitter = recognizeJitter.inMilliseconds;
      if (jitter > 0) await Future<void>.delayed(Duration(milliseconds: _random.nextInt(jitter)));
      final hello = await _step('ble_client', {'conn': d.device, 'step': 'hello'});
      final answer = await _send(d.device, hello?['data'], within: recognizeWithin);
      final peer = answer == null ? null : await _step('ble_client', {'conn': d.device, 'step': 'recognize', 'data': base64Encode(answer)});
      if (peer == null || peer['refused'] == true) {
        failed = 'refused';
        return;
      }
      d
        ..suffix = peer['suffix'] as String?
        ..alias = peer['alias'] as String?
        ..fingerprint = peer['fingerprint'] as String?;
      debugPrint('ble: ${d.device} is ${d.name}, ${clock.elapsedMilliseconds} ms');
      // Its connection closed: kept open, the other's recognition of this device hung over it
      // (7 November). Touching it connects again.
      unawaited(ble.disconnect(d.device));
      for (final old in _devices.values.where((o) => o != d && o.suffix == d.suffix).toList()) {
        _devices.remove(old.device);
        unawaited(ble.disconnect(old.device));
      }
      notifyListeners();
    } on _RadioLost catch (e) {
      // Out of reach, or busy: its connection closed, tried again later.
      failed = e.why;
      unawaited(ble.disconnect(d.device));
    } on PlatformException catch (e) {
      failed = e.message ?? e.code;
      unawaited(ble.disconnect(d.device));
    } finally {
      if (failed != null) {
        debugPrint('ble: ${d.device} not recognized ($failed), ${clock.elapsedMilliseconds} ms');
        final soon = failed != 'timeout' && failed != 'refused' && d.failures < retrySoonAfter.length;
        d.retryAt = DateTime.now().add(soon ? retrySoonAfter[d.failures] : retryAfter);
        d.failures++;
      }
      _recognizing.remove(d.device);
      scheduleMicrotask(_recognizeNext);
    }
  }

  void _set(BleStage stage, [String? failure]) {
    if (stage != BleStage.waiting) {
      _joinWait?.cancel();
      _joinWait = null;
    }
    _stage = stage;
    _failure = failure;
    _mark(failure == null ? stage.name : '${stage.name} $failure');
    // Failed: the connection closed, for the other to forget this exchange; a new touch starts
    // afresh (the second attempt was refused at once, real test of 7 November).
    if (stage == BleStage.failed && _target != null) unawaited(ble.disconnect(_target!.device));
    notifyListeners();
  }

  // How long each step takes (WP-D.6): from the touch, or from the first message of the device
  // that touched this one, to the session. Logged once the session opens, or the exchange fails.
  Stopwatch? _clock;
  final _marks = <String>[];

  /// The steps of the last exchange, with their time in ms since it began.
  String get lastTimings => _marks.join(', ');

  void _mark(String what) {
    final clock = _clock;
    if (clock == null) return;
    _marks.add('$what ${clock.elapsedMilliseconds}');
    if (what == 'session' || what.startsWith('failed')) {
      debugPrint('ble: $lastTimings');
      _clock = null;
    }
  }

  void _startClock(String what) {
    // One left by an exchange that ended without a session: a minute is longer than any.
    if (_clock case final c? when c.elapsed < const Duration(minutes: 1)) return;
    _clock = Stopwatch()..start();
    _marks.clear();
    _mark(what);
  }

  /// Touches [d]: of mutual trust, the network and the session follow; unknown, it shows a PIN,
  /// which this user types ([typePin], stage [BleStage.pinNeeded]).
  ///
  /// The radio dropping an exchange, the touch runs again by itself ([touchTries]); still
  /// dropped, it fails, `far` when the signal is weak.
  Future<void> connect(BleDevice d) async {
    if (_stage != BleStage.idle && _stage != BleStage.failed) return;
    _target = d;
    _clock = null;
    _startClock('touched');
    for (var tried = 1; ; tried++) {
      _set(BleStage.asking);
      try {
        return await _touch(d);
      } on _RadioLost catch (e) {
        if (_target != d) return; // given up meanwhile
        if (tried >= touchTries) return _set(BleStage.failed, d.weak ? 'far' : e.why);
        _mark('retry ${e.why}');
        await ble.disconnect(d.device);
        await Future<void>.delayed(touchRetryAfter);
        if (_target != d) return;
      } on PlatformException catch (e) {
        return _set(BleStage.failed, e.message ?? e.code);
      }
    }
  }

  /// One touch: to the PIN asked (unknown), or to the network and the session (mutual trust).
  Future<void> _touch(BleDevice d) async {
    final own = await ble.wifiBands();
    // A fresh challenge on this connection: the one of the recognition may be past its minute.
    final hello = await _step('ble_client', {'conn': d.device, 'step': 'hello'});
    final answer = await _send(d.device, hello?['data']);
    final peer = answer == null ? null : await _step('ble_client', {'conn': d.device, 'step': 'recognize', 'data': base64Encode(answer)});
    if (peer == null || peer['refused'] == true) return _set(BleStage.failed, 'refused');
    if (peer['trusted'] == true) {
      final req = await _step('ble_client', {'conn': d.device, 'step': 'trusted', 'create': own.create, 'join': own.join});
      _set(BleStage.confirming);
      final reply = await _send(d.device, req?['data']);
      if (reply == null) return _set(BleStage.failed, 'refused');
      await _reply(d, reply, trusted: true);
      return;
    }
    final wanted = await _step('ble_client', {'conn': d.device, 'step': 'pin_wanted'});
    final ready = await _send(d.device, wanted?['data']);
    if (ready == null) return _set(BleStage.failed, 'refused');
    final r = await _step('ble_client', {'conn': d.device, 'step': 'pin_ready', 'data': base64Encode(ready)});
    if (r?['ready'] != true) return _set(BleStage.failed, 'refused');
    _set(BleStage.pinNeeded);
  }

  /// The PIN the other device shows, typed by this user.
  Future<void> typePin(String pin) async {
    final d = _target;
    if (d == null || _stage != BleStage.pinNeeded) return;
    _set(BleStage.confirming);
    try {
      final own = await ble.wifiBands();
      final p = await _step('ble_client', {'conn': d.device, 'step': 'pin', 'pin': pin});
      final reply = await _send(d.device, p?['data']);
      if (reply == null) return _set(BleStage.failed, 'refused');
      final confirm = await _step('ble_client', {'conn': d.device, 'step': 'pin_reply', 'data': base64Encode(reply), 'create': own.create, 'join': own.join});
      if (confirm == null || confirm['refused'] == true) return _set(BleStage.failed, 'refused'); // a wrong PIN
      // The other creates its network meanwhile, when it is the one to: its answer waits for it.
      final answer = await _send(d.device, confirm['data']);
      if (answer == null) return _set(BleStage.failed, 'refused');
      await _reply(d, answer, trusted: false);
    } on _RadioLost catch (e) {
      // Not run again: the PIN would be another.
      _set(BleStage.failed, d.weak ? 'far' : e.why);
    } on PlatformException catch (e) {
      _set(BleStage.failed, e.message ?? e.code);
    }
  }

  Future<void> _reply(BleDevice d, Uint8List answer, {required bool trusted}) async {
    final r = await _step('ble_client', {'conn': d.device, 'step': 'reply', 'data': base64Encode(answer)});
    if (r == null || r['refused'] == true) return _set(BleStage.failed, 'refused');
    final fingerprint = '${r['fingerprint']}';
    switch ((r['outcome'] as num?)?.toInt()) {
      case 1: // the other created its network: join it, then connect
        _set(BleStage.connecting);
        if (r['network'] case final Map<String, Object?> network) {
          await _joinAndConnect(network, fingerprint, trusted: trusted);
        }
      case 2: // this device creates it, at the band the other agreed
        _set(BleStage.networking);
        final net = await connection.networkForBle(fiveGhz: ((r['band'] as num?)?.toInt() ?? 0) == _band5);
        _mark(net == null ? 'no network' : 'network ${net.band?.name ?? 'created'}');
        if (net?.ssid == null || net?.address == null) return _set(BleStage.failed, 'no_network');
        final n = await _step('ble_client', {'conn': d.device, 'step': 'network', 'network': _networkOf(net!)});
        final ack = await _send(d.device, n?['data']);
        final done = ack == null ? null : await _step('ble_client', {'conn': d.device, 'step': 'ack', 'data': base64Encode(ack)});
        if (done?['done'] != true) return _set(BleStage.failed, 'refused');
        // The other joins this network, then connects (pair_ble): said meanwhile, for the user not
        // to take the still moment for a failure and touch again (real test of 9 November).
        _set(BleStage.waiting);
        _joinWait = Timer(joinWithin, () {
          if (_stage == BleStage.waiting) _set(BleStage.failed, 'not_joined');
        });
      default:
        _set(BleStage.failed, 'no_band'); // neither can join what the other creates: Wi-Fi off on both
    }
  }

  /// Joins the network the other device created, then opens the session on it.
  Future<void> _joinAndConnect(Map<String, Object?> network, String fingerprint, {required bool trusted}) async {
    final n = LinkNetwork(
      ssid: '${network['ssid']}',
      passphrase: '${network['passphrase']}',
      security: (network['security'] as num?)?.toInt() ?? 1,
      band: (network['band'] as num?)?.toInt() == _band5 ? WifiBand.ghz5 : WifiBand.ghz24,
    );
    if (!await connection.joinForBle(n)) return _set(BleStage.failed, 'join');
    _mark('joined ${n.band?.name}');
    final addrs = [for (final a in (network['addrs'] as List?) ?? const []) '$a'];
    final ip = addrs.firstWhere((a) => !a.contains(':'), orElse: () => addrs.isEmpty ? '' : addrs.first);
    final port = (network['port'] as num?)?.toInt() ?? 0;
    if (ip.isEmpty || port == 0) return _set(BleStage.failed, 'join');
    await connection.pairAfterBle(ip, port, fingerprint, trusted: trusted);
    _set(BleStage.idle);
  }

  /// Gives up the exchange this device started.
  void reset() {
    _target = null;
    _set(BleStage.idle);
  }

  @override
  void dispose() {
    _joinWait?.cancel();
    _forget?.cancel();
    _afterQuiet?.cancel();
    _nextTry?.cancel();
    connection.removeListener(_follow);
    unawaited(_events.cancel());
    super.dispose();
  }
}
