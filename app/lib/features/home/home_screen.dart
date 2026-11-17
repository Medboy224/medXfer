/// The home screen of « Version 4 » (plan J8, WP-8.1): the name, the tabs as pills, the
/// selection over the whole screen, and a bar floating above it: the basket, the transfer
/// running, or Connect.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../app_state.dart';
import '../../l10n/app_localizations.dart';
import '../../library/selection.dart';
import '../../platform/file_picker.dart' as fp;
import '../../platform/library.dart';
import '../../theme.dart';
import '../../transfer/history.dart' show Direction;
import '../../transfer/transfers.dart';
import '../../widgets/drop_zone.dart';
import '../../widgets/format.dart';
import '../../widgets/v4.dart';
import '../connect/connect_screen.dart';
import '../connect/dialogs.dart';
import '../dev/dev_panel.dart';
import '../explorer/explorer_tabs.dart';
import '../history/history_tab.dart';
import '../settings/settings_screen.dart';
import '../transfer/transfer_screen.dart';
import '../trusted/trusted_screen.dart';
import '../webshare/web_share_screen.dart';

class HomeScreen extends StatelessWidget {
  const HomeScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final tabs = [
      (l.tabHistory, Icons.history_rounded),
      (l.tabApps, Icons.apps_rounded),
      (l.tabPhotos, Icons.image_outlined),
      (l.tabVideos, Icons.smart_display_outlined),
      (l.tabMusic, Icons.music_note_outlined),
      (l.tabFiles, Icons.folder_outlined),
    ];
    return DefaultTabController(
      length: tabs.length,
      initialIndex: 1,
      child: Scaffold(
        // The bar floats: the grids run under it (their bottom padding leaves room).
        extendBody: true,
        appBar: AppBar(
          titleSpacing: 16,
          title: Text(l.appTitle, style: titleStyle(23, weight: FontWeight.w900, color: c.text).copyWith(fontStyle: FontStyle.italic)),
          actions: [
            IconButton(tooltip: l.scanQr, icon: const Icon(Icons.qr_code_scanner_rounded), onPressed: () => _joinByQr(context)),
            PopupMenuButton<String>(
              key: const Key('home-menu'),
              tooltip: l.menu,
              icon: const Icon(Icons.more_vert_rounded),
              offset: const Offset(0, 48),
              onSelected: (v) => switch (v) {
                'trusted' => _open(context, const TrustedScreen()),
                'browser' => _open(context, const WebShareScreen()),
                'dev' => DevPanel.open(Navigator.of(context)),
                _ => _open(context, const SettingsScreen()),
              },
              itemBuilder: (_) => [
                _menuItem(context, 'browser', Icons.public_rounded, l.webShareTitle, key: const Key('menu-browser')),
                _menuItem(context, 'trusted', Icons.verified_user_outlined, l.trustedDevices),
                _menuItem(context, 'settings', Icons.tune_rounded, l.settings),
                if (AppScope.of(context, listen: false).settings.devMode) _menuItem(context, 'dev', Icons.code_rounded, l.devPanel),
              ],
            ),
            const SizedBox(width: 4),
          ],
          bottom: TabBar(
            isScrollable: true,
            padding: const EdgeInsets.fromLTRB(9, 0, 9, 8),
            labelPadding: const EdgeInsets.symmetric(horizontal: 17),
            indicatorPadding: const EdgeInsets.symmetric(horizontal: 3),
            tabs: [
              for (final (label, icon) in tabs)
                Tab(
                  height: 44,
                  child: Row(mainAxisSize: MainAxisSize.min, children: [Icon(icon, size: 18), const SizedBox(width: 7), Text(label)]),
                ),
            ],
          ),
        ),
        body: ListenableBuilder(
          listenable: AppScope.of(context).state.connection,
          builder: (context, child) => DropZone(
            enabled: AppScope.of(context).state.connection.session != null,
            hint: l.dropHere,
            onPaths: (paths) => unawaited(AppScope.of(context, listen: false).state.transfers.send(paths)),
            child: child!,
          ),
          child: Column(
            children: [
              const _CoreLine(),
              const _SessionBanner(),
              WebShareBanner(onOpen: () => _open(context, const WebShareScreen())),
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 0, 6, 0),
                child: Row(
                  children: [
                    Expanded(
                      child: Text(l.onThisDevice, style: TextStyle(color: c.muted, fontWeight: FontWeight.w700, fontSize: 13)),
                    ),
                    const _SelectAll(),
                  ],
                ),
              ),
              Expanded(
                child: TabBarView(
                  children: [
                    const HistoryTab(),
                    if (AppScope.of(context).state.library.supported) ...[
                      const AppsTab(),
                      const MediaGridTab(kind: MediaKind.images),
                      const MediaGridTab(kind: MediaKind.video),
                      const MusicTab(),
                      FilesTab(onSystemPicker: () => unawaited(_sendPicked(context))),
                    ] else
                      for (final _ in tabs.skip(1))
                        Center(
                          child: Padding(
                            padding: const EdgeInsets.all(32),
                            child: Text(l.desktopTabs, textAlign: TextAlign.center, style: TextStyle(color: c.muted)),
                          ),
                        ),
                  ],
                ),
              ),
            ],
          ),
        ),
        bottomNavigationBar: SafeArea(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(10, 0, 10, 12),
            child: DecoratedBox(
              key: const Key('bottom-bar'),
              decoration: BoxDecoration(
                color: c.section,
                borderRadius: BorderRadius.circular(22),
                boxShadow: [BoxShadow(color: c.shadow, blurRadius: 30, offset: const Offset(0, 10))],
              ),
              child: Padding(
                padding: const EdgeInsets.all(8),
                child: ListenableBuilder(
                  listenable: Listenable.merge([
                    AppScope.of(context).state.connection,
                    AppScope.of(context).state.transfers,
                    AppScope.of(context).state.selection,
                  ]),
                  builder: (context, _) => _bar(context),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  static PopupMenuItem<String> _menuItem(BuildContext context, String value, IconData icon, String label, {Key? key}) => PopupMenuItem(
    value: value,
    key: key,
    height: 52,
    child: Row(
      children: [
        Icon(icon, size: 20, color: context.colors.accent),
        const SizedBox(width: 12),
        Flexible(child: Text(label)),
      ],
    ),
  );

  /// What the floating bar holds: the transfer running, the basket, or the next step.
  Widget _bar(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final state = AppScope.of(context).state;
    final connection = state.connection;
    final a = state.transfers.active;
    if (a != null && a.stage != Stage.done) {
      final see = _TransferLine(active: a, onOpen: () => _open(context, const TransferScreen()));
      if (!a.lost || connection.session != null) return see;
      // The session lost for good: connecting again lets the transfer go on.
      return Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          see,
          const SizedBox(height: 8),
          SizedBox(
            width: double.infinity,
            child: FilledButton.icon(
              key: const Key('connect'),
              onPressed: () => _open(context, const ConnectScreen()),
              icon: const Icon(Icons.link_rounded),
              label: Text(l.connect),
            ),
          ),
        ],
      );
    }
    // Something picked in the explorer: the basket, and send (or connect first).
    if (!state.selection.isEmpty) return _Basket(connected: connection.session != null);
    final s = connection.session;
    if (s != null) {
      return Row(
        children: [
          const SizedBox(width: 10),
          Expanded(child: _Who(label: l.connectedToLabel, name: s.name.isEmpty ? s.ip : s.name)),
          if (state.filePicker.supported) ...[
            // A whole folder: the desktops (Android: with the explorer of WP-B.9).
            if (state.filePicker.folders) ...[
              IconButton.outlined(
                key: const Key('send-folder'),
                tooltip: l.sendFolder,
                style: IconButton.styleFrom(side: BorderSide(color: c.border, width: 2)),
                onPressed: () async {
                  final folder = await state.filePicker.folder();
                  if (folder != null) await state.transfers.send([folder]);
                },
                icon: const Icon(Icons.drive_folder_upload_rounded),
              ),
              const SizedBox(width: 6),
            ],
            Flexible(
              child: FilledButton.icon(
                key: const Key('send-files'),
                onPressed: () async => state.transfers.sendPicked(await state.filePicker.files()),
                icon: const Icon(Icons.north_east_rounded),
                iconAlignment: IconAlignment.end,
                label: Text(l.sendFiles, maxLines: 1, overflow: TextOverflow.ellipsis),
              ),
            ),
          ] else
            // Without a picker: the session's end.
            OutlinedButton.icon(
              key: const Key('disconnect'),
              onPressed: () => unawaited(connection.disconnect()),
              icon: const Icon(Icons.link_off_rounded),
              label: Text(l.disconnect),
            ),
        ],
      );
    }
    return Row(
      children: [
        const SizedBox(width: 10),
        Expanded(
          child: Text(l.noDeviceConnected, style: TextStyle(color: c.muted, fontWeight: FontWeight.w600, fontSize: 14)),
        ),
        Flexible(
          child: FilledButton.icon(
            key: const Key('connect'),
            onPressed: () => _open(context, const ConnectScreen()),
            icon: const Icon(Icons.link_rounded),
            label: Text(l.connect, maxLines: 1, overflow: TextOverflow.ellipsis),
          ),
        ),
      ],
    );
  }

  /// The system's picker, then send (the Files tab without the access to all files).
  static Future<void> _sendPicked(BuildContext context) async {
    final state = AppScope.of(context, listen: false).state;
    if (state.connection.session == null) return _open(context, const ConnectScreen());
    await state.transfers.sendPicked(await state.filePicker.files());
  }

  static void _open(BuildContext context, Widget screen) {
    unawaited(Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => screen)));
  }

  /// Android scans with the camera; a desktop has the link pasted instead.
  static Future<void> _joinByQr(BuildContext context) async {
    if (AppScope.of(context, listen: false).state.qrScanner.supported) return scanAndJoin(context);
    final link = await askLink(context);
    if (link != null && context.mounted) await joinByLink(context, link);
  }
}

/// A small label over a name: « Connecté à » / « PC Naruto ».
class _Who extends StatelessWidget {
  const _Who({required this.label, required this.name});

  final String label;
  final String name;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(label, style: TextStyle(color: c.muted, fontSize: 13)),
        Text(name, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 15)),
      ],
    );
  }
}

/// The transfer running, in the floating bar: its direction, name, progress; a tap opens it.
class _TransferLine extends StatelessWidget {
  const _TransferLine({required this.active, required this.onOpen});

  final ActiveTransfer active;
  final VoidCallback onOpen;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final a = active;
    return InkWell(
      key: const Key('see-transfer'),
      borderRadius: BorderRadius.circular(controlRadius),
      onTap: onOpen,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(4, 2, 4, 4),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Row(
              children: [
                DirectionBadge(received: a.direction == Direction.received),
                const SizedBox(width: 10),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(a.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontWeight: FontWeight.w700, fontSize: 14)),
                      Text(
                        '${(a.fraction * 100).toStringAsFixed(0)} % · ${formatBytes(context, a.totalBytes)}',
                        style: TextStyle(color: c.muted, fontSize: 13, fontFeatures: tabular),
                      ),
                    ],
                  ),
                ),
                const Icon(Icons.chevron_right_rounded),
              ],
            ),
            const SizedBox(height: 8),
            ClipRRect(
              borderRadius: BorderRadius.circular(3),
              child: LinearProgressIndicator(value: a.fraction.clamp(0, 1).toDouble(), minHeight: 6, backgroundColor: c.border, color: c.accent),
            ),
          ],
        ),
      ),
    );
  }
}

/// One line on the core while it starts or if it failed; once ready, nothing (the name shows in
/// Connect).
class _CoreLine extends StatelessWidget {
  const _CoreLine();

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return ListenableBuilder(
      listenable: state,
      builder: (context, _) {
        final (text, color) = switch (state.status) {
          CoreStatus.starting => (l.coreStarting, c.muted),
          CoreStatus.ready => (l.coreReady(state.deviceName), c.ok),
          CoreStatus.failed => (l.coreFailed(state.error ?? ''), c.danger),
        };
        // Ready: the room goes to the selection (V4); the name shows in Connect.
        if (state.status == CoreStatus.ready) return const SizedBox(width: double.infinity, height: 4);
        return Container(
          width: double.infinity,
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
          child: Row(
            children: [
              Icon(Icons.circle, size: 10, color: color),
              const SizedBox(width: 8),
              Expanded(child: Text(text, key: const Key('core-line'), style: TextStyle(color: c.muted, fontSize: 13))),
            ],
          ),
        );
      },
    );
  }
}

/// Who this device is connected to; both send and receive while it lasts.
class _SessionBanner extends StatelessWidget {
  const _SessionBanner();

  @override
  Widget build(BuildContext context) {
    final connection = AppScope.of(context).state.connection;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return ListenableBuilder(
      listenable: connection,
      builder: (context, _) {
        final s = connection.session;
        return AnimatedSize(
          duration: MediaQuery.of(context).disableAnimations ? Duration.zero : const Duration(milliseconds: 220),
          curve: Curves.easeOutCubic,
          child: s == null
              ? const SizedBox(width: double.infinity)
              : Container(
                  key: const Key('session-banner'),
                  width: double.infinity,
                  margin: const EdgeInsets.fromLTRB(12, 0, 12, 8),
                  padding: const EdgeInsets.fromLTRB(12, 6, 4, 6),
                  decoration: BoxDecoration(color: c.soft, borderRadius: BorderRadius.circular(controlRadius)),
                  child: Row(
                    children: [
                      Icon(connection.reconnecting ? Icons.sync_problem_rounded : (s.mutual ? Icons.verified_user_rounded : Icons.link_rounded), color: c.accent),
                      const SizedBox(width: 10),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(l.connectedTo(s.name.isEmpty ? s.ip : s.name), style: TextStyle(fontWeight: FontWeight.w800, color: c.text)),
                            Text(connection.reconnecting ? l.reconnecting : s.ip, style: TextStyle(color: c.muted, fontSize: 12)),
                          ],
                        ),
                      ),
                      PopupMenuButton<String>(
                        key: const Key('session-menu'),
                        tooltip: l.menu,
                        onSelected: (v) => unawaited(v == 'trust' ? connection.offerTrust() : connection.disconnect()),
                        itemBuilder: (_) => [
                          if (!s.mutual) PopupMenuItem(value: 'trust', child: Text(l.offerTrust)),
                          PopupMenuItem(value: 'disconnect', child: Text(l.disconnect)),
                        ],
                      ),
                    ],
                  ),
                ),
        );
      },
    );
  }
}

/// « Tout sélectionner » and its square box: picks every item of the tab on screen, or unpicks them.
class _SelectAll extends StatelessWidget {
  const _SelectAll();

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final tabs = DefaultTabController.of(context);
    return ListenableBuilder(
      listenable: Listenable.merge([tabs, state.selection, state.explorer]),
      builder: (context, _) {
        final picks = state.library.supported ? state.explorer.picksOf(tabs.index) : const <Pick>[];
        final all = state.selection.allOf(picks);
        return BoxToggle(
          key: const Key('select-all'),
          label: l.selectAll,
          on: all,
          onPressed: picks.isEmpty
              ? null
              : () {
                  state.selection.toggleAll(picks);
                  state.explorer.sizeFolders(state.selection);
                },
        );
      },
    );
  }
}

/// The basket: how many items, how large; to a browser, then send (or connect first).
class _Basket extends StatelessWidget {
  const _Basket({required this.connected});

  final bool connected;

  Future<void> _send(BuildContext context) async {
    final state = AppScope.of(context, listen: false).state;
    final l = AppLocalizations.of(context);
    final messenger = ScaffoldMessenger.of(context);
    switch (state.selection.plan()) {
      case Mixed():
        messenger
          ..clearSnackBars()
          ..showSnackBar(SnackBar(content: Text(l.mixedNotice)));
        return;
      case ByPath(:final paths):
        await state.transfers.send(paths);
      case ByDescriptor(:final files):
        Future<fp.Picked> openThem() async => fp.Picked(files: await state.library.open(files));
        await state.transfers.sendPicked(await openThem(), reopen: openThem);
    }
    state.selection.clear();
  }

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final sel = state.selection;
    return Row(
      key: const Key('basket'),
      children: [
        IconButton(tooltip: l.clearSelection, icon: const Icon(Icons.close_rounded), onPressed: sel.clear),
        Expanded(
          child: Text(
            l.basketLine(sel.count, formatBytes(context, sel.bytes)),
            key: const Key('basket-line'),
            maxLines: 2,
            style: TextStyle(fontWeight: FontWeight.w800, fontSize: 15, fontFeatures: tabular, fontVariations: wide),
          ),
        ),
        // A guest without medXfer: its browser (J7). Sending to a device with it stays first.
        IconButton.outlined(
          key: const Key('basket-browser'),
          tooltip: l.webShareTitle,
          style: IconButton.styleFrom(side: BorderSide(color: c.border, width: 2)),
          icon: const Icon(Icons.public_rounded),
          onPressed: () => unawaited(
            offerSelectionToBrowser(context, open: () => unawaited(Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => const WebShareScreen())))),
          ),
        ),
        const SizedBox(width: 6),
        Flexible(
          child: FilledButton.icon(
          key: const Key('basket-send'),
          onPressed: () =>
              connected ? unawaited(_send(context)) : unawaited(Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => const ConnectScreen()))),
          icon: Icon(connected ? Icons.north_east_rounded : Icons.link_rounded),
          iconAlignment: connected ? IconAlignment.end : IconAlignment.start,
          label: Text(connected ? l.sendSelection : l.connect, maxLines: 1, overflow: TextOverflow.ellipsis),
        ),
        ),
      ],
    );
  }
}
