/// What the other device can ask of this user at any time, on any screen: read out a PIN
/// (PAIR-11), accept mutual trust (PAIR-10); and what the user is told once: a pairing that
/// failed, a key that changed (ID-05), a trust gained or lost.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../connection/connection.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';

/// Wraps the app (MaterialApp.builder): it opens its windows through [navigator] and shows
/// its messages through [messenger], which sit below it.
class ConnectionPrompts extends StatefulWidget {
  const ConnectionPrompts({super.key, required this.connection, required this.navigator, required this.messenger, required this.child});

  final Connection connection;
  final GlobalKey<NavigatorState> navigator;
  final GlobalKey<ScaffoldMessengerState> messenger;
  final Widget child;

  @override
  State<ConnectionPrompts> createState() => _ConnectionPromptsState();
}

class _ConnectionPromptsState extends State<ConnectionPrompts> {
  StreamSubscription<ConnectionNotice>? _notices;
  bool _pinOpen = false;
  bool _trustOpen = false;

  Connection get _c => widget.connection;

  @override
  void initState() {
    super.initState();
    _c.addListener(_changed);
    _notices = _c.notices.listen(_notice);
  }

  @override
  void dispose() {
    _c.removeListener(_changed);
    _notices?.cancel();
    super.dispose();
  }

  BuildContext? get _dialogContext => widget.navigator.currentContext;

  void _changed() {
    final context = _dialogContext;
    if (context == null) return;
    if (_c.askedPin != null && !_pinOpen) {
      _pinOpen = true;
      showDialog<void>(context: context, barrierDismissible: false, builder: (_) => _AskedPinDialog(connection: _c))
          .whenComplete(() => _pinOpen = false);
    }
    if (_c.trustOffer != null && !_trustOpen) {
      _trustOpen = true;
      showDialog<void>(context: context, barrierDismissible: false, builder: (_) => _TrustOfferDialog(connection: _c))
          .whenComplete(() => _trustOpen = false);
    }
  }

  void _notice(ConnectionNotice n) {
    final context = _dialogContext;
    if (context == null) return;
    final l = AppLocalizations.of(context);
    if (n is KeyChanged) {
      showDialog<void>(context: context, builder: (_) => _KeyChangedDialog(detail: n.message));
      return;
    }
    final text = switch (n) {
      Paired(:final name) => l.noticePaired(name),
      PairFailed() => l.noticePairFailed,
      ConnectError(:final message) => message,
      Disconnected(:final byPeer) => byPeer ? l.noticeDisconnectedPeer : l.noticeDisconnected,
      TrustEstablished(:final name) => l.noticeTrustEstablished(name),
      TrustDeclined(:final noAnswer) => noAnswer ? l.noticeTrustNoAnswer : l.noticeTrustDeclined,
      TrustIncomplete(:final name, :final local) => local ? l.noticeTrustIncompleteLocal(name) : l.noticeTrustIncompleteRemote(name),
      HotspotFailed(:final permission, :final reason) => permission ? l.noticeHotspotPermission : l.noticeHotspotFailed(reason),
      JoinFailed(:final ssid) => l.noticeJoinFailed(ssid),
      KeyChanged() => '',
    };
    widget.messenger.currentState
      ?..clearSnackBars()
      ..showSnackBar(SnackBar(content: Text(text), behavior: SnackBarBehavior.floating));
  }

  @override
  Widget build(BuildContext context) => widget.child;
}

/// Closes itself when the model no longer holds what it shows.
mixin _ClosesWhenGone<T extends StatefulWidget> on State<T> {
  Connection get connection;
  bool get gone;

  @override
  void initState() {
    super.initState();
    connection.addListener(_check);
  }

  @override
  void dispose() {
    connection.removeListener(_check);
    super.dispose();
  }

  bool _closed = false;

  void _check() {
    if (gone) close();
  }

  /// Once only: the route may still be animating out when the model changes again.
  void close() {
    if (_closed || !mounted) return;
    _closed = true;
    // Its own route, not the top one: a screen may have opened over it meanwhile.
    final route = ModalRoute.of(context);
    if (route != null && route.isActive) Navigator.of(context).removeRoute(route);
  }
}

/// The PIN a device asked for: this user reads it out to the person at the other device.
class _AskedPinDialog extends StatefulWidget {
  const _AskedPinDialog({required this.connection});

  final Connection connection;

  @override
  State<_AskedPinDialog> createState() => _AskedPinDialogState();
}

class _AskedPinDialogState extends State<_AskedPinDialog> with _ClosesWhenGone {
  @override
  Connection get connection => widget.connection;

  @override
  bool get gone => connection.askedPin == null;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    return ListenableBuilder(
      listenable: connection,
      builder: (context, _) {
        final p = connection.askedPin;
        return AlertDialog(
          title: Text(l.askedPinTitle),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (p != null && p.stopped)
                Text(l.pinStopped, style: TextStyle(color: context.colors.danger))
              else ...[
                // Its name in this device's list when it is there (medXfer-xxxx), else its address.
                Text(l.askedPinBody(connection.nameAt(p?.from ?? ''))),
                const SizedBox(height: 16),
                SelectableText(p?.pin ?? '', key: const Key('asked-pin'), style: const TextStyle(fontSize: 38, fontWeight: FontWeight.w800, letterSpacing: 6)),
              ],
            ],
          ),
          actions: [
            // Refused, the PIN no longer pairs: the other device fails when it types it.
            if (p != null && !p.stopped)
              TextButton(
                key: const Key('refuse-pin'),
                onPressed: () => unawaited(connection.refuseAskedPin()),
                child: Text(l.refuse, style: TextStyle(color: context.colors.danger)),
              ),
            TextButton(onPressed: connection.dismissAskedPin, child: Text(l.close)),
          ],
        );
      },
    );
  }
}

/// The other device offers mutual trust: the key is shown to compare (PAIR-10).
class _TrustOfferDialog extends StatefulWidget {
  const _TrustOfferDialog({required this.connection});

  final Connection connection;

  @override
  State<_TrustOfferDialog> createState() => _TrustOfferDialogState();
}

class _TrustOfferDialogState extends State<_TrustOfferDialog> with _ClosesWhenGone {
  @override
  Connection get connection => widget.connection;

  @override
  bool get gone => connection.trustOffer == null;

  late final _offer = connection.trustOffer;
  Timer? _deadline;

  @override
  void initState() {
    super.initState();
    final left = _offer?.deadline.difference(DateTime.now());
    // Past the deadline, the core counts it as a "no": the window goes.
    if (left != null) _deadline = Timer(left, connection.dropTrustOffer);
  }

  @override
  void dispose() {
    _deadline?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final offer = _offer;
    return AlertDialog(
      title: Text(l.trustOfferTitle(offer?.deviceName ?? '')),
      content: Text(l.trustOfferBody(offer?.fingerprint ?? '')),
      actions: [
        TextButton(key: const Key('trust-decline'), onPressed: () => unawaited(connection.answerTrust(false)), child: Text(l.decline)),
        FilledButton(key: const Key('trust-accept'), onPressed: () => unawaited(connection.answerTrust(true)), child: Text(l.accept)),
      ],
    );
  }
}

/// ID-05: a known device came back with another key. Said plainly, with the core's details.
class _KeyChangedDialog extends StatelessWidget {
  const _KeyChangedDialog({required this.detail});

  final String detail;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return AlertDialog(
      icon: Icon(Icons.gpp_bad_rounded, color: c.danger, size: 40),
      title: Text(l.keyChangedTitle),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(l.keyChangedBody),
          const SizedBox(height: 12),
          Text(l.details, style: const TextStyle(fontWeight: FontWeight.w700)),
          SelectableText(detail, key: const Key('key-changed-detail'), style: TextStyle(color: c.muted, fontSize: 13)),
        ],
      ),
      actions: [FilledButton(onPressed: () => Navigator.of(context).pop(), child: Text(l.close))],
    );
  }
}
