/// The home screen of the validated mockups (§1.2 of the J6b plan): tabs at the top, the
/// "On this device" row, the banner of the session, and the Connect button. The tabs' content
/// comes with WP-B.9.
library;

import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../app_state.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';
import '../connect/connect_screen.dart';
import '../connect/dialogs.dart';
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
              onSelected: (v) => v == 'trusted' ? _open(context, const TrustedScreen()) : _soon(context),
              itemBuilder: (_) => [
                PopupMenuItem(value: 'trusted', child: Text(l.trustedDevices)),
                PopupMenuItem(value: 'settings', child: Text(l.settings)),
              ],
            ),
          ],
          bottom: TabBar(isScrollable: true, tabs: [for (final t in tabs) Tab(text: t)]),
        ),
        body: Column(
          children: [
            const _CoreLine(),
            const _SessionBanner(),
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
            key: const Key('bottom-bar'),
            decoration: BoxDecoration(color: c.surface, border: Border(top: BorderSide(color: c.border))),
            padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
            // heightFactor 1: centered, never stretched to the height the Scaffold offers.
            child: Align(
              heightFactor: 1,
              child: ListenableBuilder(
                listenable: AppScope.of(context).state.connection,
                builder: (context, _) {
                  final connection = AppScope.of(context).state.connection;
                  // Connected: the bottom bar becomes the sending one with WP-B.3.
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

  static void _open(BuildContext context, Widget screen) {
    unawaited(Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => screen)));
  }

  /// Android scans with the camera (WP-B.2b); a desktop has the link pasted instead.
  static Future<void> _joinByQr(BuildContext context) async {
    if (Platform.isAndroid) return _open(context, const ConnectScreen());
    final connection = AppScope.of(context, listen: false).state.connection;
    final link = await askLink(context);
    if (link != null) await connection.pairUri(link);
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
