/// Keeps the app alive in the background while something runs (API-08, WP-B.4): the Connect
/// screen open, a session, an offer waiting, a transfer, the hotspot, another's network joined. Android's foreground
/// service then holds the network, and its notification says what runs; once nothing does,
/// it goes. It reads the models of the connection and of the transfers.
library;

import 'dart:async';
import 'dart:ui';

import 'package:flutter/foundation.dart';

import 'connection/connection.dart';
import 'l10n/app_localizations.dart';
import 'platform/background.dart';
import 'transfer/history.dart';
import 'transfer/transfers.dart';

/// What the notification says.
@immutable
class Notice {
  const Notice(this.title, this.text, [this.progress]);

  final String title;
  final String text;
  final int? progress;

  @override
  bool operator ==(Object other) => other is Notice && other.title == title && other.text == text && other.progress == progress;

  @override
  int get hashCode => Object.hash(title, text, progress);
}

class BackgroundKeeper {
  BackgroundKeeper({required this.background, required this.connection, required this.transfers, AppLocalizations Function()? strings})
    : _strings = strings ?? _systemStrings {
    if (!background.supported) return;
    connection.addListener(_update);
    transfers.addListener(_update);
  }

  final Background background;
  final Connection connection;
  final Transfers transfers;
  final AppLocalizations Function() _strings;

  Notice? _shown;
  bool _askedNotifications = false;
  Timer? _throttle;
  bool _pending = false;

  /// The notification at most once a second: a transfer's progress comes ten times as often.
  static const minInterval = Duration(seconds: 1);

  /// What should run now, or null.
  @visibleForTesting
  Notice? wanted() {
    final l = _strings();
    final a = transfers.active;
    if (a != null && a.stage != Stage.done) {
      final title = a.direction == Direction.sent ? l.sendingTo(a.device) : l.receivingFrom(a.device);
      if (a.stage == Stage.offered) return Notice(title, l.waitingAnswer(a.device));
      final percent = (a.fraction * 100).round();
      return Notice(title, a.paused ? l.pausedLabel : l.notifProgress('$percent', l.unitMB(a.speedMBps.toStringAsFixed(1))), percent);
    }
    final o = transfers.offer;
    if (o != null) return Notice('medXfer', l.notifOffer(o.device));
    final s = connection.session;
    if (s != null) return Notice(l.connectedTo(s.name.isEmpty ? s.ip : s.name), l.notifSession);
    if (connection.myHotspot != null) return Notice('medXfer', l.notifHotspot);
    // Another's network, lost with its session, joined again: without the service, Android cut
    // the network of the app in the background, and nothing came back (J6c).
    if (connection.joining) return Notice('medXfer', l.reconnecting);
    if (connection.showing) return Notice('medXfer', l.notifVisible);
    return null;
  }

  void _update() {
    if (_throttle != null) {
      _pending = true;
      return;
    }
    _apply();
    _throttle = Timer(minInterval, () {
      _throttle = null;
      if (_pending) {
        _pending = false;
        _update();
      }
    });
  }

  void _apply() {
    final want = wanted();
    if (want == _shown) return;
    final was = _shown;
    _shown = want;
    if (want == null) {
      unawaited(background.release());
      return;
    }
    if (was == null && !_askedNotifications) {
      // Asked once, when something first runs: refused, the service runs all the same.
      _askedNotifications = true;
      unawaited(background.askNotifications());
    }
    unawaited(background.hold(title: want.title, text: want.text, progress: want.progress));
  }

  void dispose() {
    _throttle?.cancel();
    connection.removeListener(_update);
    transfers.removeListener(_update);
  }

  static AppLocalizations _systemStrings() {
    final locale = PlatformDispatcher.instance.locale;
    final supported = AppLocalizations.supportedLocales.any((l) => l.languageCode == locale.languageCode);
    return lookupAppLocalizations(supported ? Locale(locale.languageCode) : const Locale('en'));
  }
}
