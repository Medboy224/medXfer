/// Watches the wired cards of a PC (WP-E.2 and WP-E.3 of the cable plan): every
/// [CableWatch.period], [CableLink.check] gives a link-local address to a cable Linux left
/// without one, and [CableLink.manualAddress] finds a wired card whose address is set by hand.
/// The screen says both ([CableWatch.given], [CableWatch.manual]).
library;

import 'dart:async';

import 'package:flutter/foundation.dart';

import '../platform/cable.dart';

class CableWatch extends ChangeNotifier {
  CableWatch(this.link, {this.period = const Duration(seconds: 5)});

  final CableLink link;
  final Duration period;

  Timer? _timer;
  bool _busy = false;
  Set<String> _given = const {};

  /// The wired cards that have the app's address now.
  Set<String> get given => _given;

  /// A wired card with a link whose address is set by hand, or null.
  ManualAddress? get manual => _manual;
  ManualAddress? _manual;

  /// Starts watching; nothing where the system gives the cable its address (Windows) or has
  /// no cable (the phones).
  void start() {
    if (link is NoCableLink || _timer != null) return;
    unawaited(_tick());
    _timer = Timer.periodic(period, (_) => unawaited(_tick()));
  }

  Future<void> _tick() async {
    if (_busy) return; // a profile coming up takes a few seconds: one check at a time
    _busy = true;
    try {
      final given = await link.check();
      final manual = await link.manualAddress();
      if (!setEquals(given, _given) || manual?.card != _manual?.card || manual?.address != _manual?.address) {
        _given = given;
        _manual = manual;
        notifyListeners();
      }
    } catch (_) {
      // nmcli missing or refused: no address given, nothing else changes.
    } finally {
      _busy = false;
    }
  }

  /// Stops watching and takes back the addresses given: the app closes.
  Future<void> stop() async {
    _timer?.cancel();
    _timer = null;
    await link.release();
    if (_given.isNotEmpty) {
      _given = const {};
      notifyListeners();
    }
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }
}
