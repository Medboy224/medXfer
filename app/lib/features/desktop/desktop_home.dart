/// The desktop layout (mockup « 10. Bureau », §1.2 of the J6b plan): a side menu and its pages.
/// Envoyer: files and folders dropped or chosen, then the device to send them to. Recevoir: how
/// this computer is found, where files go. Then the history, the trusted devices, the settings.
/// The phone's tabs are the other layout (HomeScreen).
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';
import '../../transfer/transfers.dart';
import '../../widgets/format.dart';
import '../history/history_tab.dart';
import '../settings/settings_screen.dart';
import '../transfer/transfer_screen.dart';
import '../trusted/trusted_screen.dart';
import 'receive_page.dart';
import 'send_page.dart';

enum DesktopPage { send, receive, history, trusted, settings }

class DesktopHome extends StatefulWidget {
  const DesktopHome({super.key});

  @override
  State<DesktopHome> createState() => _DesktopHomeState();
}

class _DesktopHomeState extends State<DesktopHome> {
  DesktopPage _page = DesktopPage.send;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    return Scaffold(
      body: Row(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _SideMenu(page: _page, onPick: (p) => setState(() => _page = p)),
          Expanded(
            child: switch (_page) {
              DesktopPage.send => const SendPage(),
              DesktopPage.receive => const ReceivePage(),
              DesktopPage.history => Scaffold(
                appBar: AppBar(title: Text(l.tabHistory)),
                body: const HistoryTab(),
              ),
              DesktopPage.trusted => const TrustedScreen(),
              DesktopPage.settings => const SettingsScreen(),
            },
          ),
        ],
      ),
    );
  }
}

/// The menu of the mockup: the app's name, this computer's, the pages; below, the session and
/// the transfer under way.
class _SideMenu extends StatelessWidget {
  const _SideMenu({required this.page, required this.onPick});

  final DesktopPage page;
  final ValueChanged<DesktopPage> onPick;

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final accent = Theme.of(context).colorScheme.primary;
    final labels = {
      DesktopPage.send: l.navSend,
      DesktopPage.receive: l.navReceive,
      DesktopPage.history: l.tabHistory,
      DesktopPage.trusted: l.trustedDevices,
      DesktopPage.settings: l.settings,
    };
    return Container(
      key: const Key('side-menu'),
      width: 240,
      padding: const EdgeInsets.fromLTRB(16, 24, 16, 16),
      decoration: BoxDecoration(
        color: c.surface,
        border: Border(right: BorderSide(color: c.border)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const Padding(
            padding: EdgeInsets.fromLTRB(8, 0, 8, 4),
            child: Text('medXfer', style: TextStyle(fontSize: 22, fontWeight: FontWeight.w800, letterSpacing: -0.4)),
          ),
          ListenableBuilder(
            listenable: state,
            builder: (context, _) => Padding(
              padding: const EdgeInsets.fromLTRB(8, 0, 8, 16),
              child: Text(l.thisDevice(state.deviceName), style: TextStyle(color: c.muted, fontSize: 13)),
            ),
          ),
          for (final p in DesktopPage.values)
            Padding(
              padding: const EdgeInsets.only(bottom: 4),
              child: Material(
                color: p == page ? c.chip : Colors.transparent,
                borderRadius: BorderRadius.circular(12),
                child: InkWell(
                  key: ValueKey('nav-${p.name}'),
                  borderRadius: BorderRadius.circular(12),
                  onTap: () => onPick(p),
                  child: Container(
                    constraints: const BoxConstraints(minHeight: 44),
                    padding: const EdgeInsets.symmetric(horizontal: 12),
                    alignment: Alignment.centerLeft,
                    child: Text(
                      labels[p]!,
                      style: TextStyle(fontWeight: FontWeight.w600, fontSize: 15, color: p == page ? accent : c.text),
                    ),
                  ),
                ),
              ),
            ),
          const Spacer(),
          const _Status(),
        ],
      ),
    );
  }
}

/// The session and the transfer under way, at the foot of the menu: one click to its screen.
class _Status extends StatelessWidget {
  const _Status();

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return ListenableBuilder(
      listenable: Listenable.merge([state.connection, state.transfers]),
      builder: (context, _) {
        final session = state.connection.session;
        final a = state.transfers.active;
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (a != null && a.stage != Stage.done)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: FilledButton.tonal(
                  key: const Key('see-transfer'),
                  style: FilledButton.styleFrom(minimumSize: const Size(0, 44), shape: const StadiumBorder()),
                  onPressed: () => unawaited(Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => const TransferScreen()))),
                  child: Text(
                    '${a.name} · ${(a.fraction * 100).toStringAsFixed(0)} % · ${formatBytes(context, a.totalBytes)}',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ),
            if (session != null)
              Row(
                children: [
                  Icon(Icons.link_rounded, size: 18, color: c.muted),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      state.connection.reconnecting ? l.reconnecting : l.connectedTo(session.name),
                      key: const Key('session-line'),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(color: c.muted, fontSize: 13),
                    ),
                  ),
                  IconButton(
                    key: const Key('disconnect'),
                    tooltip: l.disconnect,
                    icon: const Icon(Icons.link_off_rounded, size: 18),
                    onPressed: () => unawaited(state.connection.disconnect()),
                  ),
                ],
              ),
          ],
        );
      },
    );
  }
}
