/// The connection between this device and another (WP-B.2 of the J6b plan): the devices
/// nearby, this device's QR code and PIN, pairing by each path, the open session, and mutual
/// trust. It speaks only to the [Core]; the screens read it and call its actions.
library;

import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

import '../core/core.dart';
import '../platform/hotspot.dart';
import '../platform/network.dart';
import 'pair_link.dart';

/// A device found on the network (`peers_list`).
@immutable
class NearbyDevice {
  const NearbyDevice({required this.name, required this.ip, required this.port, this.trusted = false});

  factory NearbyDevice.fromJson(Map<String, Object?> m) {
    final alias = (m['trusted_alias'] as String?) ?? '';
    return NearbyDevice(
      // A device of mutual trust under the name recorded here; any other under the generic
      // name it announces, medXfer-xxxx (DISC-07). Its address names no one.
      name: alias.isNotEmpty ? alias : (m['device_name'] as String?) ?? '',
      ip: (m['host_ip'] as String?) ?? '',
      port: (m['port'] as num?)?.toInt() ?? 0,
      trusted: alias.isNotEmpty,
    );
  }

  final String name;
  final String ip;
  final int port;
  final bool trusted;

  String get target => port > 0 ? '$ip:$port' : ip;
}

/// This device's QR code (`pair_show`): single use, until [expires].
@immutable
class QrInvitation {
  const QrInvitation({required this.uri, required this.expires, required this.instance});

  final String uri;
  final DateTime expires;

  /// The name under which the other device sees this one (DISC-07).
  final String instance;
}

/// A PIN this device shows: on its own (`pair_pin_shown`), or because a device asked for one
/// (`pin_requested`, PAIR-11). Empty and [stopped] after too many wrong PINs.
@immutable
class ShownPin {
  const ShownPin({required this.pin, required this.expires, this.address = '', this.from = '', this.instance = '', this.stopped = false});

  factory ShownPin.fromJson(Map<String, Object?> m) => ShownPin(
        pin: (m['pin'] as String?) ?? '',
        expires: _time(m['expires']),
        address: (m['address'] as String?) ?? '',
        from: (m['from'] as String?) ?? '',
        instance: (m['instance'] as String?) ?? '',
        stopped: m['stopped'] == true,
      );

  final String pin;
  final DateTime expires;

  /// Where the other device reaches this one (pair_pin_shown), or who asked (pin_requested).
  final String address;
  final String from;
  final String instance;
  final bool stopped;
}

/// The open session: both devices send and receive as long as it lasts.
@immutable
class PeerSession {
  const PeerSession({required this.name, required this.ip, this.fingerprint = '', this.mutual = false});

  final String name;
  final String ip;
  final String fingerprint;

  /// A device of mutual trust: the next time, no PIN or QR code is needed.
  final bool mutual;

  PeerSession copyWith({String? name, String? fingerprint, bool? mutual}) => PeerSession(
        name: name ?? this.name,
        ip: ip,
        fingerprint: fingerprint ?? this.fingerprint,
        mutual: mutual ?? this.mutual,
      );
}

/// The other device offers mutual trust (`trust_offered`): answer before [deadline].
@immutable
class TrustOffer {
  const TrustOffer({required this.deviceName, required this.fingerprint, required this.deadline});

  final String deviceName;
  final String fingerprint;
  final DateTime deadline;
}

/// A trusted device, as the core keeps it (`trusted_devices`).
@immutable
class TrustedDevice {
  const TrustedDevice({required this.fingerprint, required this.short, required this.alias, this.lastAddress = ''});

  factory TrustedDevice.fromJson(Map<String, Object?> m) => TrustedDevice(
        fingerprint: (m['fingerprint'] as String?) ?? '',
        short: (m['short'] as String?) ?? '',
        alias: (m['alias'] as String?) ?? '',
        lastAddress: (m['last_address'] as String?) ?? '',
      );

  final String fingerprint;
  final String short;
  final String alias;
  final String lastAddress;
}

/// Something to tell the user once: a pairing that failed, a trust that changed.
sealed class ConnectionNotice {
  const ConnectionNotice();
}

class Paired extends ConnectionNotice {
  const Paired(this.name);
  final String name;
}

/// The pairing failed; on purpose, the core says nothing more (PAIR-07).
class PairFailed extends ConnectionNotice {
  const PairFailed();
}

/// A known device came back with another key: the connection was refused (ID-05).
class KeyChanged extends ConnectionNotice {
  const KeyChanged(this.message);
  final String message;
}

class ConnectError extends ConnectionNotice {
  const ConnectError(this.message);
  final String message;
}

class Disconnected extends ConnectionNotice {
  const Disconnected({required this.byPeer});

  /// The other device left, or the link was lost; false: this user asked.
  final bool byPeer;
}

class TrustEstablished extends ConnectionNotice {
  const TrustEstablished(this.name);
  final String name;
}

class TrustDeclined extends ConnectionNotice {
  const TrustDeclined({required this.noAnswer});
  final bool noAnswer;
}

/// The trust is on one side only (PAIR-09): [local] when this device still trusts the other.
class TrustIncomplete extends ConnectionNotice {
  const TrustIncomplete({required this.name, required this.local});
  final String name;
  final bool local;
}

/// The access point could not be created: the permission refused, or Android said no (the
/// system's hotspot on, no channel…).
class HotspotFailed extends ConnectionNotice {
  const HotspotFailed({required this.permission, this.reason = ''});
  final bool permission;
  final String reason;
}

/// The other device's access point could not be joined (refused by the user, not found).
class JoinFailed extends ConnectionNotice {
  const JoinFailed(this.ssid);
  final String ssid;
}

class Connection extends ChangeNotifier {
  Connection(this.core, {this.network = const NoNetworkInfo(), this.hotspot = const NoHotspot()}) {
    _sub = core.events.listen(take);
    // Another Wi-Fi, a hotspot gone: the core gets the interfaces again (DISC-10).
    _netSub = network.changes.listen((_) => unawaited(pushNetwork()));
  }

  StreamSubscription<void>? _netSub;

  final Core core;

  /// What the core cannot see by itself on Android: the interfaces, the multicast lock.
  final NetworkInfo network;

  /// The access point this device creates, or joins (DISC-12, WP-B.7).
  final Hotspot hotspot;
  HotspotInfo? _myHotspot;
  bool _gaveNet = false; // a desktop gave the core its own network's interface (pushNetwork)
  LinkNetwork? _joined;
  bool _hotspotBusy = false;
  Timer? _scanLimit;
  late final StreamSubscription<CoreEvent> _sub;
  final _notices = StreamController<ConnectionNotice>.broadcast();

  /// The requests of this model whose errors are the user's to see.
  final _mine = <String>{};

  List<NearbyDevice> _nearby = const [];
  bool _scanning = false;
  QrInvitation? _invitation;
  ShownPin? _pin;
  ShownPin? _askedPin;
  String? _pinNeeded;
  PeerSession? _session;
  bool _reconnecting = false;
  TrustOffer? _trustOffer;
  List<TrustedDevice> _trusted = const [];
  bool _showing = false;
  bool _codeOpen = false;
  String _instance = '';

  /// This user refused a PIN asked for, until then: the failure it causes here is no news.
  DateTime? _refusedUntil;

  bool _remember = true;

  /// Offer mutual trust once paired (PAIR-10): the other device's user is asked.
  bool get remember => _remember;

  /// The QR code and PIN on screen carry the choice: new ones are drawn.
  Future<void> setRemember(bool value) async {
    if (value == _remember) return;
    _remember = value;
    notifyListeners();
    if (_codeOpen) await showCode();
  }

  /// The generic name this device announces, medXfer-xxxx (DISC-07): the one a device not yet
  /// trusted sees in its list. The Connect screen shows it, so the other user picks this one.
  String get instance => _instance;

  /// The status the application asked for at start: a reply, which [take] does not see.
  void takeStatus(CoreEvent status) => take(status);

  /// The name of the device at ip in the list, else its address: who asks for a PIN.
  String nameAt(String ip) {
    for (final d in _nearby) {
      if (d.ip == ip) return d.name;
    }
    return ip;
  }

  /// The access point this device created: its name and passphrase go in the QR code.
  HotspotInfo? get myHotspot => _myHotspot;

  /// Creating or joining an access point is under way.
  bool get hotspotBusy => _hotspotBusy;

  /// A screen shows this device's code (openCode).
  bool get codeOpen => _codeOpen;

  /// What the QR code shows: the core's link, with the network when this device created one.
  String? get qrData {
    final inv = _invitation;
    if (inv == null || inv.uri.isEmpty) return null;
    final h = _myHotspot;
    if (h == null || (h.ssid ?? '').isEmpty) return inv.uri;
    return PairLink.withNetwork(inv.uri, LinkNetwork(ssid: h.ssid!, passphrase: h.passphrase ?? '', security: h.security));
  }

  /// The Connect screen is open: this device announces itself.
  bool get showing => _showing;

  List<NearbyDevice> get nearby => _nearby;
  bool get scanning => _scanning;
  QrInvitation? get invitation => _invitation;

  /// The PIN this device shows on the Connect screen.
  ShownPin? get pin => _pin;

  /// The PIN a device asked for (PAIR-11): this user reads it out to the other one.
  ShownPin? get askedPin => _askedPin;

  /// The device that asks this user to type its PIN (`pin_needed`): its address.
  String? get pinNeeded => _pinNeeded;
  PeerSession? get session => _session;
  bool get reconnecting => _reconnecting;
  TrustOffer? get trustOffer => _trustOffer;
  List<TrustedDevice> get trusted => _trusted;

  /// One-shot messages for the user.
  Stream<ConnectionNotice> get notices => _notices.stream;

  // Actions.

  /// The Connect screen opens: this device announces itself (DISC-05: only when the user
  /// asks) and looks for devices nearby. It shows no QR code: a QR code exists only with an
  /// access point, which it carries, so scanning one works wherever the devices are.
  Future<void> openConnect() async {
    _showing = true;
    // The network may have changed since the last time (another Wi-Fi, a hotspot).
    await pushNetwork();
    await network.multicast(true);
    await _call('set_discoverable', {'on': true});
    await scan();
  }

  /// A screen shows this device's code: the access point's QR code and PIN, or the address
  /// and PIN typed by hand when a device does not see this one in its list.
  Future<void> openCode() async {
    _codeOpen = true;
    await showCode();
  }

  void closeCode() {
    _codeOpen = false;
    _invitation = null;
    _pin = null;
    notifyListeners();
  }

  /// The Connect screen closes: this device stops announcing itself. An access point nobody
  /// joined goes with it; one that carries the session stays until the session ends.
  Future<void> closeConnect() async {
    _showing = false;
    _scanLimit?.cancel();
    _scanning = false;
    _invitation = null;
    _pin = null;
    notifyListeners();
    await _call('set_discoverable', {'on': false});
    await network.multicast(false);
    if (_myHotspot != null && _session == null) await stopHotspot();
  }

  /// Gives the core the interfaces the platform sees (DISC-10). While this device's access point
  /// runs, that interface alone: the QR code, the PIN and the announcements then carry the
  /// address the joining device reaches (probe of WP-B.7). A desktop's core enumerates by
  /// itself, except while the computer's own network runs (WP-C.2 of the J6c plan): it gets
  /// that interface, then the enumeration back.
  Future<void> pushNetwork() async {
    var interfaces = await network.interfaces();
    if (interfaces == null) {
      final net = _myHotspot?.net;
      if (net != null) {
        _gaveNet = true;
        await _call('set_network_info', {'interfaces': [net.toJson()]});
      } else if (_gaveNet) {
        _gaveNet = false;
        await _call('set_network_info', {'enumerate': true});
      }
      return;
    }
    if (interfaces.isEmpty) return;
    final ap = _myHotspot?.address;
    if (ap != null && ap.isNotEmpty) {
      final only = [for (final i in interfaces) if (i.addrs.any((a) => a.split('/').first == ap)) i];
      if (only.isNotEmpty) interfaces = only;
    }
    // A network joined by a QR code: the core's own sockets go through it (pkg/netbind, J6c);
    // asked each time, its handle changes when it is joined again after a loss.
    final joined = _joined != null ? await hotspot.joinedNetwork() : 0;
    await _call('set_network_info', {
      'interfaces': [for (final i in interfaces) i.toJson()],
      if (joined != 0) 'network': joined,
    });
  }

  /// Creates this device's access point (Android), then a QR code and PIN that carry it.
  Future<void> startHotspot() async {
    if (!hotspot.supported || _hotspotBusy || _myHotspot != null) return;
    _hotspotBusy = true;
    notifyListeners();
    try {
      if (!await hotspot.permission()) {
        _notices.add(const HotspotFailed(permission: true));
        return;
      }
      _myHotspot = await hotspot.start();
      await pushNetwork();
      if (_codeOpen) await showCode();
    } on PlatformException catch (e) {
      _myHotspot = null;
      _notices.add(HotspotFailed(permission: false, reason: e.message ?? e.code));
    } finally {
      _hotspotBusy = false;
      notifyListeners();
    }
  }

  /// Stops this device's access point; the core gets every interface back.
  Future<void> stopHotspot() async {
    if (_myHotspot == null) return;
    _myHotspot = null;
    notifyListeners();
    await hotspot.stop();
    await pushNetwork();
    if (_codeOpen) await showCode();
  }

  /// Pairs by a scanned or pasted link. When it names an access point, this device joins it
  /// first (Android; on a desktop the user joins it in the system's settings beforehand).
  Future<void> pairLink(PairLink link) async {
    final n = link.network;
    if (n != null && hotspot.canJoin && _joined?.ssid != n.ssid) {
      _hotspotBusy = true;
      notifyListeners();
      try {
        if (!await hotspot.permission()) {
          _notices.add(const HotspotFailed(permission: true));
          return;
        }
        await hotspot.join(n.ssid, n.passphrase, security: n.security);
        _joined = n;
        await pushNetwork();
      } on PlatformException {
        _notices.add(JoinFailed(n.ssid));
        return;
      } finally {
        _hotspotBusy = false;
        notifyListeners();
      }
    }
    await pairUri(link.uri);
  }

  /// Leaves the access point joined by [pairLink]: back to the usual network.
  Future<void> leaveJoined() async {
    if (_joined == null) return;
    _joined = null;
    await hotspot.leave();
    await pushNetwork();
  }

  /// A new QR code and PIN; the previous ones stop working.
  Future<void> showCode() async {
    await _call('pair_show', {if (remember) 'trust': true});
    await _call('pair_pin_show', {if (remember) 'trust': true});
  }

  Future<void> scan() async {
    _scanning = true;
    notifyListeners();
    // The list stays as it was if no answer comes: the spinner stops all the same.
    _scanLimit?.cancel();
    _scanLimit = Timer(scanLimit, () {
      if (!_scanning) return;
      _scanning = false;
      notifyListeners();
    });
    await _call('scan');
  }

  /// How long a scan may show as running.
  static const scanLimit = Duration(seconds: 20);

  /// Pairs with a device of the list: directly with a device of mutual trust; any other
  /// shows a PIN on its screen, which this user types ([pinNeeded]).
  Future<void> pairWith(NearbyDevice d) => _call('pair', {'ip': d.ip, if (d.port > 0) 'port': d.port, if (remember) 'trust': true});

  /// Pairs with the device whose QR code was scanned, or whose link was pasted.
  Future<void> pairUri(String uri) => _call('pair_uri', {'uri': uri.trim(), if (remember) 'trust': true});

  /// Pairs with the device at target (address, or address:port) by the PIN it shows.
  Future<void> pairPin(String target, String pin) {
    _pinNeeded = null;
    notifyListeners();
    return _call('pair_pin', {'target': target.trim(), 'pin': pin, if (remember) 'trust': true});
  }

  void cancelPinNeeded() {
    _pinNeeded = null;
    notifyListeners();
  }

  /// The user refuses the device that asked for a PIN: the PIN no longer pairs (pin_refuse).
  Future<void> refuseAskedPin() async {
    _askedPin = null;
    _refusedUntil = DateTime.now().add(const Duration(minutes: 2)); // a PIN's life (PAIR-02)
    notifyListeners();
    await _call('pin_refuse');
  }

  /// The user read the PIN out, or does not want to.
  void dismissAskedPin() {
    _askedPin = null;
    notifyListeners();
  }

  Future<void> disconnect() => _call('disconnect');

  Future<void> offerTrust() => _call('trust_offer');

  Future<void> answerTrust(bool accept) {
    _trustOffer = null;
    notifyListeners();
    return _call('trust_answer', {'accept': accept});
  }

  /// Past its deadline, the offer counts as declined by the core: forget it here too.
  void dropTrustOffer() {
    if (_trustOffer == null) return;
    _trustOffer = null;
    notifyListeners();
  }

  Future<void> loadTrusted() => _call('trust_list');

  Future<void> forget(TrustedDevice d) => _call('trust_forget', {'fingerprint': d.fingerprint});

  Future<void> rename(TrustedDevice d, String name) => _call('trust_rename', {'fingerprint': d.fingerprint, 'name': name.trim()});

  Future<void> _call(String action, [Map<String, Object?>? payload]) async {
    final reply = await core.call(action, payload);
    if (reply.id != null) _mine.add(reply.id!);
    for (final e in reply.events) {
      take(e);
    }
    if (!reply.ok && reply.error != null && reply.first('action_error') == null) {
      _error(reply.error!);
    }
  }

  // Events: the replies of the actions and what the core tells everyone, alike.

  @visibleForTesting
  void take(CoreEvent e) {
    final m = e.map;
    switch (e.name) {
      case 'peers_list':
        _nearby = [
          for (final p in (e.data as List?) ?? const [])
            if (p is Map<String, Object?>) NearbyDevice.fromJson(p),
        ]..sort((a, b) => a.trusted == b.trusted ? a.name.compareTo(b.name) : (a.trusted ? -1 : 1));
        _scanning = false;
        _scanLimit?.cancel();
      case 'pair_show':
        if (!_codeOpen) return;
        _invitation = QrInvitation(
          uri: (m['uri'] as String?) ?? '',
          expires: _time(m['expires']),
          instance: (m['instance'] as String?) ?? '',
        );
      case 'pair_pin_shown':
        if (!_codeOpen) return;
        _pin = ShownPin.fromJson(m);
      case 'pin_requested':
        _askedPin = ShownPin.fromJson(m);
      case 'pin_needed':
        _pinNeeded = (m['target'] as String?) ?? '';
      case 'paired':
        final ip = (m['ip'] as String?) ?? '';
        final name = (m['device_name'] as String?) ?? '';
        final before = _session;
        // A second paired (pair_accept) brings the name only: keep what the first one said.
        _session = before != null && before.ip == ip
            ? before.copyWith(
                name: name.isEmpty ? null : name,
                fingerprint: m['fingerprint'] as String?,
                mutual: m['mutual'] as bool?,
              )
            : PeerSession(name: name, ip: ip, fingerprint: (m['fingerprint'] as String?) ?? '', mutual: m['mutual'] == true);
        _reconnecting = false;
        _pinNeeded = null;
        _askedPin = null;
        if (before == null) _notices.add(Paired(_session!.name));
      case 'pair_failed':
        final refused = _refusedUntil;
        if (refused != null && DateTime.now().isBefore(refused)) {
          _refusedUntil = null; // the other device typed the refused PIN: this user said no
          return;
        }
        _notices.add(const PairFailed());
      case 'reconnecting':
        _reconnecting = true;
      case 'disconnected':
        if (_session != null) {
          _notices.add(Disconnected(byPeer: (m['reason'] as String?) != 'user requested disconnect'));
        }
        _session = null;
        _reconnecting = false;
        _trustOffer = null;
        // The network made for this session is no longer needed.
        if (_myHotspot != null && !_showing) unawaited(stopHotspot());
        if (_joined != null) unawaited(leaveJoined());
      case 'status':
        _instance = (m['instance'] as String?) ?? _instance;
        if (m['paired'] == true && _session == null) {
          _session = PeerSession(name: (m['paired_device'] as String?) ?? '', ip: (m['paired_ip'] as String?) ?? '');
        } else if (m['paired'] == false && _session != null) {
          _session = null;
        }
      case 'trust_offered':
        final seconds = (m['timeout_s'] as num?)?.toInt() ?? 60;
        _trustOffer = TrustOffer(
          deviceName: (m['device_name'] as String?) ?? '',
          fingerprint: (m['fingerprint'] as String?) ?? '',
          deadline: DateTime.now().add(Duration(seconds: seconds)),
        );
      case 'trust_established':
        _session = _session?.copyWith(mutual: true);
        _trustOffer = null;
        _notices.add(TrustEstablished((m['device_name'] as String?) ?? _session?.name ?? ''));
        unawaited(loadTrusted());
      case 'trust_declined':
        _trustOffer = null;
        _notices.add(TrustDeclined(noAnswer: m['reason'] == 'no_answer'));
      case 'trust_incomplete':
        _notices.add(TrustIncomplete(name: (m['device_name'] as String?) ?? '', local: m['side'] == 'local'));
      case 'trusted_devices':
        _trusted = [
          for (final d in (m['devices'] as List?) ?? const [])
            if (d is Map<String, Object?>) TrustedDevice.fromJson(d),
        ];
      case 'action_error':
        final error = (m['error'] as String?) ?? '';
        // A late error can arrive before the call that caused it has returned its id: the
        // refusal of ID-05 is shown whatever request it answers.
        if (!error.contains('REFUSED:') && (e.id == null || !_mine.contains(e.id))) return;
        _scanning = false;
        _error(error);
      default:
        return;
    }
    notifyListeners();
  }

  void _error(String message) {
    // ID-05: a known device that shows another key. The core's message names it and says
    // what to do.
    final i = message.indexOf('REFUSED:');
    _notices.add(i >= 0 ? KeyChanged(message.substring(i + 'REFUSED:'.length).trim()) : ConnectError(message));
  }

  @override
  void dispose() {
    _scanLimit?.cancel();
    _netSub?.cancel();
    _sub.cancel();
    _notices.close();
    super.dispose();
  }
}

DateTime _time(Object? unix) => DateTime.fromMillisecondsSinceEpoch(((unix as num?)?.toInt() ?? 0) * 1000);
