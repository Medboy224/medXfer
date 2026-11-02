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

  /// Its generic name's 4 hex digits (DISC-07): the same name as on the network.
  String? suffix;

  /// When of mutual trust: the name this device gave it, and its key.
  String? alias;
  String? fingerprint;

  bool get recognized => suffix != null;
  bool get trusted => fingerprint != null;
  String get name => alias ?? 'medXfer-$suffix';
}

/// Where the exchange this device started stands.
enum BleStage { idle, asking, pinNeeded, confirming, networking, connecting, failed }

/// Bands as the core takes them: bit 0 2.4 GHz, bit 1 5 GHz.
const _band5 = 2;

class BleLink extends ChangeNotifier {
  BleLink(this.core, this.ble, this.connection) {
    _events = ble.events.listen(_take);
  }

  final Core core;
  final Ble ble;
  final Connection connection;
  late final StreamSubscription<BleEvent> _events;

  bool _running = false;
  String? _problem;
  final _devices = <String, BleDevice>{};
  final _recognizing = <String>{};
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
  List<BleDevice> get devices => _devices.values.where((d) => d.recognized).toList()
    ..sort((a, b) => a.trusted != b.trusted ? (a.trusted ? -1 : 1) : b.rssi.compareTo(a.rssi));

  BleDevice? get target => _target;
  BleStage get stage => _stage;

  /// Why the exchange failed: refused (a wrong PIN among others, PAIR-07), no_network, join, or
  /// the radio's error.
  String? get failure => _failure;

  /// Advertises and scans while the user looks for a device or waits for one (D3, DISC-05).
  Future<void> start() async {
    if (_running) return;
    _problem = await _cannotStart();
    if (_problem == null) {
      try {
        await ble.start();
        _running = true;
      } on PlatformException catch (e) {
        _problem = e.code;
      }
    }
    notifyListeners();
  }

  Future<String?> _cannotStart() async {
    if (!ble.supported) return 'unsupported';
    if (!await ble.permission()) return 'permission';
    if (!await ble.enabled()) return 'bluetooth_off';
    return null;
  }

  /// Asks the user to turn Bluetooth on (an app may not), then starts.
  Future<void> turnOn() async {
    if (await ble.turnOn()) await start();
  }

  Future<void> stop() async {
    if (!_running) return;
    _running = false;
    _devices.clear();
    _servedTrusted.clear();
    await ble.stop();
    notifyListeners();
  }

  void _take(BleEvent e) {
    switch (e) {
      case BleSeen(:final device, :final rssi):
        final d = _devices.putIfAbsent(device, () => BleDevice(device))..rssi = rssi;
        if (!d.recognized && _recognizing.add(device)) unawaited(_recognize(d));
        notifyListeners();
      case final BleMessage m:
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

  Future<Uint8List?> _send(String device, Object? data) async {
    if (data is! String) return null;
    return ble.exchange(device, base64Decode(data));
  }

  // The server side: another device touched this one.

  Future<void> _serve(BleMessage m) async {
    final own = await ble.wifiBands();
    final r = await _step('ble_server', {'conn': m.conn, 'step': 'message', 'data': base64Encode(m.data), 'create': own.create, 'join': own.join});
    if (r == null) return;
    if (r['data'] case final String data) await ble.reply(m.conn, base64Decode(data));
    if (r['request'] case final Map<String, Object?> request) await _answer(m.conn, request, own);
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
    try {
      final hello = await _step('ble_client', {'conn': d.device, 'step': 'hello'});
      final answer = await _send(d.device, hello?['data']);
      if (answer == null) return;
      final peer = await _step('ble_client', {'conn': d.device, 'step': 'recognize', 'data': base64Encode(answer)});
      if (peer == null || peer['refused'] == true) return;
      d
        ..suffix = peer['suffix'] as String?
        ..alias = peer['alias'] as String?
        ..fingerprint = peer['fingerprint'] as String?;
      for (final old in _devices.values.where((o) => o != d && o.suffix == d.suffix).toList()) {
        _devices.remove(old.device);
        unawaited(ble.disconnect(old.device));
      }
      notifyListeners();
    } on PlatformException {
      // Out of reach, or busy: seen again, it is tried again.
      _devices.remove(d.device);
    } finally {
      _recognizing.remove(d.device);
    }
  }

  void _set(BleStage stage, [String? failure]) {
    _stage = stage;
    _failure = failure;
    notifyListeners();
  }

  /// Touches [d]: of mutual trust, the network and the session follow; unknown, it shows a PIN,
  /// which this user types ([typePin], stage [BleStage.pinNeeded]).
  Future<void> connect(BleDevice d) async {
    if (_stage != BleStage.idle && _stage != BleStage.failed) return;
    _target = d;
    _set(BleStage.asking);
    try {
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
    } on PlatformException catch (e) {
      _set(BleStage.failed, e.message ?? e.code);
    }
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
        if (net?.ssid == null || net?.address == null) return _set(BleStage.failed, 'no_network');
        final n = await _step('ble_client', {'conn': d.device, 'step': 'network', 'network': _networkOf(net!)});
        final ack = await _send(d.device, n?['data']);
        final done = ack == null ? null : await _step('ble_client', {'conn': d.device, 'step': 'ack', 'data': base64Encode(ack)});
        if (done?['done'] != true) return _set(BleStage.failed, 'refused');
        _set(BleStage.idle); // the other joins this network, then connects (pair_ble)
      default:
        _set(BleStage.failed, 'no_network');
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
    unawaited(_events.cancel());
    super.dispose();
  }
}
