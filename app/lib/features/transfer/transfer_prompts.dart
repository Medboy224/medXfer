/// What a transfer asks of this user on any screen: the offer of the other device to accept or
/// decline; then the transfer screen, opened when one starts; and the messages once.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../l10n/app_localizations.dart';
import '../../platform/storage.dart';
import '../../theme.dart';
import '../../transfer/transfers.dart';
import '../../widgets/format.dart';
import 'transfer_screen.dart';

class TransferPrompts extends StatefulWidget {
  const TransferPrompts({super.key, required this.transfers, required this.navigator, required this.messenger, required this.child});

  final Transfers transfers;
  final GlobalKey<NavigatorState> navigator;
  final GlobalKey<ScaffoldMessengerState> messenger;
  final Widget child;

  @override
  State<TransferPrompts> createState() => _TransferPromptsState();
}

class _TransferPromptsState extends State<TransferPrompts> {
  StreamSubscription<TransferNotice>? _notices;
  bool _offerOpen = false;
  bool _screenOpen = false;

  /// The transfer whose screen the user left while it ran: not opened again by itself (the
  /// home screen offers to see it).
  String? _left;

  static String _key(ActiveTransfer a) => '${a.direction.name}|${a.name}|${a.started ?? a.totalBytes}';

  Transfers get _t => widget.transfers;

  @override
  void initState() {
    super.initState();
    _t.addListener(_changed);
    _notices = _t.notices.listen(_notice);
  }

  @override
  void dispose() {
    _t.removeListener(_changed);
    _notices?.cancel();
    super.dispose();
  }

  void _changed() {
    final nav = widget.navigator.currentState;
    final context = widget.navigator.currentContext;
    if (nav == null || context == null) return;
    if (_t.offer != null && !_offerOpen) {
      _offerOpen = true;
      showDialog<void>(
        context: context,
        barrierDismissible: false,
        builder: (_) => _OfferDialog(transfers: _t),
      ).whenComplete(() => _offerOpen = false);
    }
    final a = _t.active;
    if (a == null) _left = null;
    if (a != null && a.stage != Stage.done && !_screenOpen && _left != _key(a)) {
      _screenOpen = true;
      nav.push(MaterialPageRoute<void>(builder: (_) => const TransferScreen())).whenComplete(() {
        _screenOpen = false;
        final now = _t.active;
        if (now != null && now.stage != Stage.done) _left = _key(now);
        _t.dismiss();
      });
    }
  }

  void _notice(TransferNotice n) {
    final context = widget.navigator.currentContext;
    if (context == null) return;
    final l = AppLocalizations.of(context);
    final text = switch (n) {
      TransferFailed(:final message, :final noSpace) => noSpace ? l.noticeNoSpace : message,
      DiskFull() => l.noticeDiskFull,
      ReceiveFolderGone(:final folder, :final used) => l.noticeReceiveFolderGone(folder, used),
      FileError(:final file, :final error) => l.noticeFileError(file, error),
      TransferStopped(:final device, :final error) => l.noticeTransferStopped(device, error),
    };
    widget.messenger.currentState
      ?..clearSnackBars()
      ..showSnackBar(SnackBar(content: Text(text), behavior: SnackBarBehavior.floating));
  }

  @override
  Widget build(BuildContext context) => widget.child;
}

/// "X wants to send you N files (size)": accept or decline (§1.2). The lack of room is said
/// before accepting (STO-07).
class _OfferDialog extends StatefulWidget {
  const _OfferDialog({required this.transfers});

  final Transfers transfers;

  @override
  State<_OfferDialog> createState() => _OfferDialogState();
}

class _OfferDialogState extends State<_OfferDialog> {
  bool _closed = false;

  /// Android without the access to all files: the files go to Download/medXfer, and the user
  /// may allow the access here (decision J2), where it matters.
  late final ReceiveStorage _storage = AppScope.of(context, listen: false).state.storage;
  bool? _allFiles;

  @override
  void initState() {
    super.initState();
    widget.transfers.addListener(_check);
    if (_storage.supported) {
      _storage.allFiles().then((v) {
        if (mounted) setState(() => _allFiles = v);
      });
    }
  }

  Future<void> _askAccess() async {
    final granted = await _storage.askAllFiles();
    if (mounted) setState(() => _allFiles = granted);
  }

  @override
  void dispose() {
    widget.transfers.removeListener(_check);
    super.dispose();
  }

  // Answered here, or withdrawn by the other side (disconnected): the window goes.
  void _check() {
    if (widget.transfers.offer == null && !_closed && mounted) {
      _closed = true;
      // Its own route, not the top one: a screen may have opened over it meanwhile.
      final route = ModalRoute.of(context);
      if (route != null && route.isActive) Navigator.of(context).removeRoute(route);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final o = widget.transfers.offer;
    if (o == null) return const SizedBox.shrink();
    const shown = 4;
    return AlertDialog(
      title: Text(l.incomingTitle(o.device)),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            o.name,
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 16),
          ),
          Text(
            '${l.filesCount(o.files)} · ${formatBytes(context, o.bytes)}',
            key: const Key('offer-size'),
            style: TextStyle(color: c.muted),
          ),
          if (o.items.length > 1) ...[
            const SizedBox(height: 8),
            for (final i in o.items.take(shown))
              Text(
                '• ${i.path}',
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(color: c.muted, fontSize: 13),
              ),
            if (o.items.length > shown) Text(l.andMore(o.items.length - shown), style: TextStyle(color: c.muted, fontSize: 13)),
          ],
          if (!o.enoughSpace) ...[
            const SizedBox(height: 10),
            Text(
              l.noSpaceWarning(formatBytes(context, o.bytes), o.freeBytes < 0 ? '?' : formatBytes(context, o.freeBytes)),
              key: const Key('offer-no-space'),
              style: TextStyle(color: c.danger, fontWeight: FontWeight.w600),
            ),
          ],
          if (_allFiles == false) ...[
            const SizedBox(height: 10),
            Text(l.storageHint, key: const Key('storage-hint'), style: TextStyle(color: c.muted, fontSize: 13)),
            Align(
              alignment: Alignment.centerLeft,
              child: TextButton(key: const Key('storage-allow'), onPressed: _askAccess, child: Text(l.storageAllow)),
            ),
          ],
        ],
      ),
      actions: [
        TextButton(key: const Key('offer-decline'), onPressed: () => unawaited(widget.transfers.reject()), child: Text(l.decline)),
        FilledButton(key: const Key('offer-accept'), onPressed: () => unawaited(widget.transfers.accept()), child: Text(l.accept)),
      ],
    );
  }
}
