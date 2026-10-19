/// The home screen of the validated mockups (§1.2 of the J6b plan): tabs at the top, the
/// "On this device" row, and the Connect button. The tabs' content comes with WP-B.9, the
/// Connect screen with WP-B.2.
library;

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../app_state.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';

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
            IconButton(tooltip: l.scanQr, icon: const Icon(Icons.qr_code_scanner_rounded), onPressed: () => _soon(context)),
            IconButton(tooltip: l.settings, icon: const Icon(Icons.menu_rounded), onPressed: () => _soon(context)),
          ],
          bottom: TabBar(isScrollable: true, tabs: [for (final t in tabs) Tab(text: t)]),
        ),
        body: Column(
          children: [
            const _CoreLine(),
            Container(
              color: c.section,
              padding: const EdgeInsets.fromLTRB(16, 4, 4, 4),
              child: Row(
                children: [
                  Expanded(child: Text(l.onThisDevice, style: TextStyle(color: c.muted, fontWeight: FontWeight.w600))),
                  IconButton(
                    tooltip: l.selectAll,
                    icon: Icon(Icons.radio_button_unchecked_rounded, color: c.muted),
                    onPressed: () => _soon(context),
                  ),
                ],
              ),
            ),
            Expanded(
              child: TabBarView(
                children: [
                  for (final _ in tabs)
                    Center(
                      child: Padding(
                        padding: const EdgeInsets.all(32),
                        child: Text(l.comingSoon, textAlign: TextAlign.center, style: TextStyle(color: c.muted)),
                      ),
                    ),
                ],
              ),
            ),
          ],
        ),
        bottomNavigationBar: SafeArea(
          child: Container(
            decoration: BoxDecoration(color: c.surface, border: Border(top: BorderSide(color: c.border))),
            padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
            child: Center(
              child: FilledButton.icon(
                style: FilledButton.styleFrom(
                  minimumSize: const Size(0, 52),
                  padding: const EdgeInsets.symmetric(horizontal: 28),
                  shape: const StadiumBorder(),
                  textStyle: const TextStyle(fontFamily: 'Figtree', fontWeight: FontWeight.w800, fontSize: 16),
                ),
                onPressed: () => _soon(context),
                icon: const Icon(Icons.link_rounded),
                label: Text(l.connect),
              ),
            ),
          ),
        ),
      ),
    );
  }

  static void _soon(BuildContext context) {
    ScaffoldMessenger.of(context)
      ..clearSnackBars()
      ..showSnackBar(SnackBar(content: Text(AppLocalizations.of(context).comingSoon)));
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
              Expanded(child: Text(text, key: const Key('core-line'), style: TextStyle(color: c.muted, fontSize: 13))),
            ],
          ),
        );
      },
    );
  }
}
