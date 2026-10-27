/// The home screen of the validated mockups (§1.2 of the J6b plan): tabs at the top, the
/// "On this device" row, the banner of the session, and the Connect button. The tabs' content
/// comes with WP-B.9.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../app_state.dart';
import '../../l10n/app_localizations.dart';
import '../../library/desktop_picks.dart';
import '../../library/selection.dart';
import '../../platform/file_picker.dart' as fp;
import '../../platform/library.dart';
import '../../theme.dart';
import '../../transfer/transfers.dart';
import '../../widgets/drop_zone.dart';
import '../../widgets/format.dart';
import '../connect/connect_screen.dart';
import '../connect/dialogs.dart';
import '../dev/dev_panel.dart';
import '../explorer/explorer_tabs.dart';
import '../history/history_tab.dart';
import '../settings/settings_screen.dart';
import '../transfer/transfer_screen.dart';
import '../trusted/trusted_screen.dart';

class HomeScreen extends StatelessWidget {
  const HomeScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final tabs = [l.tabHistory, l.tabApps, l.tabPhotos, l.tabVideos, l.tabMusic, l.tabFiles];
    return DefaultTabController(
      length: tabs.length,
      initialIndex: 1,
      child: Scaffold(
        appBar: AppBar(
          backgroundColor: c.surface,
          surfaceTintColor: Colors.transparent,
          title: Text(l.appTitle, style: const TextStyle(fontWeight: FontWeight.w800, letterSpacing: -0.4)),
          actions: [
            IconButton(tooltip: l.scanQr, icon: const Icon(Icons.qr_code_scanner_rounded), onPressed: () => _joinByQr(context)),
            PopupMenuButton<String>(
              key: const Key('home-menu'),
              tooltip: l.menu,
              icon: const Icon(Icons.menu_rounded),
              onSelected: (v) => switch (v) {
                'trusted' => _open(context, const TrustedScreen()),
                'dev' => DevPanel.open(Navigator.of(context)),
                _ => _open(context, const SettingsScreen()),
              },
              itemBuilder: (_) => [
                PopupMenuItem(value: 'trusted', child: Text(l.trustedDevices)),
                PopupMenuItem(value: 'settings', child: Text(l.settings)),
                if (AppScope.of(context, listen: false).settings.devMode) PopupMenuItem(value: 'dev', child: Text(l.devPanel)),
              ],
            ),
          ],
          bottom: TabBar(isScrollable: true, tabs: [for (final t in tabs) Tab(text: t)]),
        ),
        body: DropZone(
          hint: l.dropHere,
          onPaths: (paths) => _addPaths(AppScope.of(context, listen: false).state, paths),
          child: Column(
            children: [
              const _CoreLine(),
              const _SessionBanner(),
              Container(
                color: c.section,
                padding: const EdgeInsets.fromLTRB(16, 4, 4, 4),
                child: Row(
                  children: [
                    Expanded(
                      child: Text(
                        l.onThisDevice,
                        style: TextStyle(color: c.muted, fontWeight: FontWeight.w600),
                      ),
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
                            child: Text(
                              l.desktopTabs,
                              textAlign: TextAlign.center,
                              style: TextStyle(color: c.muted),
                            ),
                          ),
                        ),
                  ],
                ),
              ),
            ],
          ),
        ),
        bottomNavigationBar: SafeArea(
          child: Container(
            key: const Key('bottom-bar'),
            decoration: BoxDecoration(
              color: c.surface,
              border: Border(top: BorderSide(color: c.border)),
            ),
            padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
            // heightFactor 1: centered, never stretched to the height the Scaffold offers.
            child: Align(
              heightFactor: 1,
              child: ListenableBuilder(
                listenable: Listenable.merge([
                  AppScope.of(context).state.connection,
                  AppScope.of(context).state.transfers,
                  AppScope.of(context).state.selection,
                ]),
                builder: (context, _) {
                  final state = AppScope.of(context).state;
                  final connection = state.connection;
                  final a = state.transfers.active;
                  if (a != null && a.stage != Stage.done) {
                    final see = FilledButton.tonalIcon(
                      key: const Key('see-transfer'),
                      style: FilledButton.styleFrom(minimumSize: const Size(0, 52), shape: const StadiumBorder()),
                      onPressed: () => _open(context, const TransferScreen()),
                      icon: const Icon(Icons.swap_vert_rounded),
                      label: Text(
                        '${a.name} · ${(a.fraction * 100).toStringAsFixed(0)} % · ${formatBytes(context, a.totalBytes)}',
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    );
                    if (!a.lost || connection.session != null) return see;
                    // The session lost for good: connecting again lets the transfer go on.
                    return Wrap(
                      alignment: WrapAlignment.center,
                      spacing: 8,
                      runSpacing: 8,
                      children: [
                        see,
                        FilledButton.icon(
                          key: const Key('connect'),
                          style: FilledButton.styleFrom(minimumSize: const Size(0, 52), shape: const StadiumBorder()),
                          onPressed: () => _open(context, const ConnectScreen()),
                          icon: const Icon(Icons.link_rounded),
                          label: Text(l.connect),
                        ),
                      ],
                    );
                  }
                  // Something picked in the explorer: the basket, and send (or connect first).
                  if (!state.selection.isEmpty) return _Basket(connected: connection.session != null);
                  if (connection.session != null && state.filePicker.supported) {
                    return Wrap(
                      alignment: WrapAlignment.center,
                      spacing: 8,
                      runSpacing: 8,
                      children: [
                        // A desktop fills the basket, files and folders together; Android sends.
                        FilledButton.icon(
                          key: const Key('pick-files'),
                          style: FilledButton.styleFrom(minimumSize: const Size(0, 52), shape: const StadiumBorder()),
                          onPressed: () async {
                            final picked = await state.filePicker.files();
                            if (state.filePicker.folders) return _addPaths(state, picked.paths);
                            await state.transfers.sendPicked(picked);
                          },
                          icon: const Icon(Icons.upload_file_rounded),
                          label: Text(state.filePicker.folders ? l.chooseFiles : l.sendFiles),
                        ),
                        // A whole folder: the desktops (Android: with the explorer of WP-B.9).
                        if (state.filePicker.folders)
                          OutlinedButton.icon(
                            key: const Key('pick-folder'),
                            style: OutlinedButton.styleFrom(minimumSize: const Size(0, 52), shape: const StadiumBorder()),
                            onPressed: () async => _addFolder(state),
                            icon: const Icon(Icons.drive_folder_upload_rounded),
                            label: Text(l.chooseFolder),
                          ),
                      ],
                    );
                  }
                  // Without a picker: the session's end.
                  if (connection.session != null) {
                    return OutlinedButton.icon(
                      key: const Key('disconnect'),
                      style: OutlinedButton.styleFrom(minimumSize: const Size(0, 52), shape: const StadiumBorder()),
                      onPressed: () => unawaited(connection.disconnect()),
                      icon: const Icon(Icons.link_off_rounded),
                      label: Text(l.disconnect),
                    );
                  }
                  return FilledButton.icon(
                    key: const Key('connect'),
                    style: FilledButton.styleFrom(
                      minimumSize: const Size(0, 52),
                      padding: const EdgeInsets.symmetric(horizontal: 28),
                      shape: const StadiumBorder(),
                      textStyle: const TextStyle(fontFamily: 'Figtree', fontWeight: FontWeight.w800, fontSize: 16),
                    ),
                    onPressed: () => _open(context, const ConnectScreen()),
                    icon: const Icon(Icons.link_rounded),
                    label: Text(l.connect),
                  );
                },
              ),
            ),
          ),
        ),
      ),
    );
  }

  /// Paths chosen on a desktop, or dropped on it, into the basket (WP-B.8).
  static void _addPaths(AppState state, List<String> paths) {
    state.selection.addAll(picksOfPaths(paths));
    state.explorer.sizeFolders(state.selection);
  }

  static Future<void> _addFolder(AppState state) async {
    final folder = await state.filePicker.folder();
    if (folder != null) _addPaths(state, [folder]);
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

/// One line on the core: starting, ready under its name, or why it failed.
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
        return Container(
          width: double.infinity,
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
          child: Row(
            children: [
              Icon(Icons.circle, size: 10, color: color),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  text,
                  key: const Key('core-line'),
                  style: TextStyle(color: c.muted, fontSize: 13),
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

/// Who this device is connected to; both send and receive while it lasts (§1.2).
class _SessionBanner extends StatelessWidget {
  const _SessionBanner();

  @override
  Widget build(BuildContext context) {
    final connection = AppScope.of(context).state.connection;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final accent = Theme.of(context).colorScheme.primary;
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
                  padding: const EdgeInsets.fromLTRB(14, 6, 4, 6),
                  decoration: BoxDecoration(
                    color: accent.withValues(alpha: 0.10),
                    borderRadius: BorderRadius.circular(14),
                    border: Border.all(color: accent.withValues(alpha: 0.35)),
                  ),
                  child: Row(
                    children: [
                      Icon(connection.reconnecting ? Icons.sync_problem_rounded : (s.mutual ? Icons.verified_user_rounded : Icons.link_rounded), color: accent),
                      const SizedBox(width: 10),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(
                              l.connectedTo(s.name.isEmpty ? s.ip : s.name),
                              style: TextStyle(fontWeight: FontWeight.w800, color: c.text),
                            ),
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

/// The circle of "On this device": picks every item of the tab on screen, or unpicks them.
class _SelectAll extends StatelessWidget {
  const _SelectAll();

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final tabs = DefaultTabController.of(context);
    return ListenableBuilder(
      listenable: Listenable.merge([tabs, state.selection, state.explorer]),
      builder: (context, _) {
        final picks = state.library.supported ? state.explorer.picksOf(tabs.index) : const <Pick>[];
        final all = state.selection.allOf(picks);
        return IconButton(
          key: const Key('select-all'),
          tooltip: l.selectAll,
          icon: Icon(all ? Icons.check_circle_rounded : Icons.radio_button_unchecked_rounded, color: all ? Theme.of(context).colorScheme.primary : c.muted),
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

/// The basket (§1.2): how many items, how large; then send, or connect first.
class _Basket extends StatelessWidget {
  const _Basket({required this.connected});

  final bool connected;

  Future<void> _send(BuildContext context) async {
    final state = AppScope.of(context, listen: false).state;
    final l = AppLocalizations.of(context);
    final messenger = ScaffoldMessenger.of(context);
    switch (state.selection.plan(byPath: !state.library.supported)) {
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
    final sel = state.selection;
    return Row(
      key: const Key('basket'),
      children: [
        IconButton(tooltip: l.clearSelection, icon: const Icon(Icons.close_rounded), onPressed: sel.clear),
        Expanded(
          child: Text(
            l.basketLine(sel.count, formatBytes(context, sel.bytes)),
            key: const Key('basket-line'),
            style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 16),
          ),
        ),
        // A desktop adds files or a folder to what it chose (WP-B.8).
        if (state.filePicker.folders)
          PopupMenuButton<bool>(
            key: const Key('basket-add'),
            tooltip: l.addToSelection,
            icon: const Icon(Icons.add_rounded),
            onSelected: (folder) async {
              if (folder) return HomeScreen._addFolder(state);
              HomeScreen._addPaths(state, (await state.filePicker.files()).paths);
            },
            itemBuilder: (_) => [
              PopupMenuItem(key: const Key('basket-add-files'), value: false, child: Text(l.chooseFiles)),
              PopupMenuItem(key: const Key('basket-add-folder'), value: true, child: Text(l.chooseFolder)),
            ],
          ),
        FilledButton.icon(
          key: const Key('basket-send'),
          style: FilledButton.styleFrom(minimumSize: const Size(0, 52), shape: const StadiumBorder()),
          onPressed: () =>
              connected ? unawaited(_send(context)) : unawaited(Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => const ConnectScreen()))),
          icon: Icon(connected ? Icons.send_rounded : Icons.link_rounded),
          label: Text(connected ? l.sendSelection : l.connect),
        ),
      ],
    );
  }
}
