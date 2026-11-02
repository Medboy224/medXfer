/// Watches Windows' firewall: at the start and every [FirewallWatch.period], the networks this
/// computer is on where medXfer is kept out ([FirewallWatch.blocked]); [FirewallWatch.allow]
/// asks the user to let it in, then looks again. The screen says it, with the button.
library;

import 'dart:async';

import 'package:flutter/foundation.dart';

import '../platform/firewall.dart';

class FirewallWatch extends ChangeNotifier {
  FirewallWatch(this.firewall, {this.period = const Duration(seconds: 15)});

  final Firewall firewall;
  final Duration period;

  Timer? _timer;
  List<FirewallNetwork> _blocked = const [];
  bool _asking = false;
  bool? _lastAnswer;

  /// The networks this computer is on where the firewall keeps medXfer out.
  List<FirewallNetwork> get blocked => _blocked;

  /// The permission is being asked (the system's prompt is open).
  bool get asking => _asking;

  /// What the last request gave: true let in, false refused or failed, null none yet.
  bool? get lastAnswer => _lastAnswer;

  /// Starts watching; nothing where there is no firewall to read (Linux, the phones).
  void start() {
    if (firewall is NoFirewall || _timer != null) return;
    unawaited(check());
    _timer = Timer.periodic(period, (_) => unawaited(check()));
  }

  Future<void> check() async {
    List<FirewallNetwork> networks;
    try {
      networks = await firewall.status();
    } catch (_) {
      return; // could not be read: nothing said, rather than a wrong warning
    }
    final blocked = [
      for (final n in networks)
        if (!n.allowed) n,
    ];
    if (!_same(blocked, _blocked)) {
      _blocked = blocked;
      notifyListeners();
    }
  }

  /// Asks the user (the system's prompt) to let medXfer in, then looks again.
  Future<void> allow() async {
    if (_asking) return;
    _asking = true;
    notifyListeners();
    try {
      _lastAnswer = await firewall.allow();
    } catch (_) {
      _lastAnswer = false;
    }
    _asking = false;
    notifyListeners();
    await check();
  }

  /// Stops watching: the app closes.
  void stop() {
    _timer?.cancel();
    _timer = null;
  }

  static bool _same(List<FirewallNetwork> a, List<FirewallNetwork> b) =>
      a.length == b.length && [for (var i = 0; i < a.length; i++) a[i].name == b[i].name && a[i].category == b[i].category].every((x) => x);

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }
}
