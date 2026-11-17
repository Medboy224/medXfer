/// The desktop layout (mockup « 10. Bureau », §1.2 of the J6b plan): a side menu and its pages.
/// Accueil: files and folders dropped or chosen, and the connection beside them. Then the
/// history, the trusted devices, the settings, where the receive folder is (27 October 2026: no
/// page Recevoir, a connected device sends whatever page is open). The phone's tabs are the
/// other layout (HomeScreen).
///
/// The connection is not tied to a page or to a direction (decided on 27 October 2026): while
/// the window is open, this computer announces itself and looks for the devices of its network,
/// so two devices on the same Wi-Fi see each other; once connected, either one sends.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../connection/connection.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';
import '../../transfer/transfers.dart';
import '../../widgets/format.dart';
import '../connect/dialogs.dart';
import '../history/history_tab.dart';
import '../settings/settings_screen.dart';
import '../transfer/transfer_screen.dart';
import '../trusted/trusted_screen.dart';
import '../webshare/web_share_screen.dart';
import 'send_page.dart';

enum DesktopPage { home, history, browser, trusted, settings }

class DesktopHome extends StatefulWidget {
  const DesktopHome({super.key});

  @override
  State<DesktopHome> createState() => _DesktopHomeState();
}

class _DesktopHomeState extends State<DesktopHome> {
  DesktopPage _page = DesktopPage.home;
  late final Connection _c = AppScope.of(context, listen: false).state.connection;
  Timer? _rescan;
  bool _asking = false;
  bool _hadSession = false;
  Timer? _stopLater; // this computer's network, kept after a session lost (J6c)

  late final _ws = AppScope.of(context, listen: false).state.webShare;

  /// The selection sent to a browser (the Accueil's button): its page shown.
  void _browserWanted() {
    if (!_ws.wanted || !mounted) return;
    if (_page != DesktopPage.browser) setState(() => _page = DesktopPage.browser);
    _ws.shown();
  }

  @override
  void initState() {
    super.initState();
    _c.addListener(_changed);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _ws.addListener(_browserWanted);
    });
    // After the first frame: the model tells its listeners at once, and the home builds now.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      unawaited(_c.loadTrusted());
      unawaited(_c.openConnect());
    });
    // The devices come and go: look again every 5 s, a scan lasting 2 s.
    _rescan = Timer.periodic(const Duration(seconds: 5), (_) {
      if (!_c.scanning) unawaited(_c.scan());
    });
  }

  @override
  void dispose() {
    _rescan?.cancel();
    _stopLater?.cancel();
    _c.removeListener(_changed);
    _ws.removeListener(_browserWanted);
    // Not during the unmount: the model tells its listeners, and the tree is locked now.
    scheduleMicrotask(() => unawaited(_c.closeConnect()));
    super.dispose();
  }

  /// A device of the list shows a PIN: this user types it, whatever the page. The session over,
  /// the network this computer made for it stops: the window keeps looking for devices, which
  /// on the phone the end of Connect would have done (WP-C.3 of the J6c plan). A session lost
  /// (timed out) keeps it rejoinFor: the device comes back.
  void _changed() {
    final had = _hadSession;
    _hadSession = _c.session != null;
    if (_hadSession) _stopLater?.cancel();
    if (had && !_hadSession && _c.myHotspot != null && !_c.codeOpen && !_c.hotspotInUse()) {
      if (_c.lastEndTimedOut) {
        // Lost, not ended: the other device joins this network again (J6c). Stopped at once,
        // it had nothing to come back to; it waits as long as the device tries (rejoinFor).
        _stopLater?.cancel();
        _stopLater = Timer(Connection.rejoinFor, () {
          if (mounted && _c.session == null && _c.myHotspot != null && !_c.codeOpen && !_c.hotspotInUse()) unawaited(_c.stopHotspot());
        });
      } else {
        unawaited(_c.stopHotspot());
      }
    }
    final target = _c.pinNeeded;
    if (!mounted || _c.session != null || target == null || _asking) return;
    unawaited(_askPin(target));
  }

  Future<void> _askPin(String target) async {
    _asking = true;
    final name = _c.nearby.where((d) => d.target == target || target.startsWith('${d.ip}:')).map((d) => d.name).firstOrNull;
    final answer = await askPin(context, target: target, device: name ?? target);
    _asking = false;
    if (answer == null) {
      _c.cancelPinNeeded();
      return;
    }
    await _c.pairPin(answer.$1, answer.$2);
  }

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
              DesktopPage.home => const SendPage(),
              DesktopPage.history => Scaffold(
                appBar: AppBar(title: Text(l.tabHistory)),
                body: const HistoryTab(),
              ),
              DesktopPage.browser => const WebShareScreen(page: true),
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
    final labels = {
      DesktopPage.home: l.navHome,
      DesktopPage.history: l.tabHistory,
      DesktopPage.browser: l.navBrowser,
      DesktopPage.trusted: l.trustedDevices,
      DesktopPage.settings: l.settings,
    };
    const icons = {
      DesktopPage.home: Icons.home_outlined,
      DesktopPage.history: Icons.history_rounded,
      DesktopPage.browser: Icons.public_rounded,
      DesktopPage.trusted: Icons.verified_user_outlined,
      DesktopPage.settings: Icons.tune_rounded,
    };
    return Container(
      key: const Key('side-menu'),
      width: 240,
      padding: const EdgeInsets.fromLTRB(16, 24, 16, 16),
      decoration: BoxDecoration(color: c.surface),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(8, 0, 8, 4),
            child: Text('medXfer', style: titleStyle(26, weight: FontWeight.w900).copyWith(fontStyle: FontStyle.italic)),
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
                color: p == page ? c.strong : Colors.transparent,
                borderRadius: BorderRadius.circular(controlRadius),
                child: InkWell(
                  key: ValueKey('nav-${p.name}'),
                  borderRadius: BorderRadius.circular(controlRadius),
                  onTap: () => onPick(p),
                  child: Container(
                    constraints: const BoxConstraints(minHeight: 48),
                    padding: const EdgeInsets.symmetric(horizontal: 12),
                    alignment: Alignment.centerLeft,
                    child: Row(
                      children: [
                        Icon(icons[p], size: 20, color: p == page ? c.onStrong : c.muted),
                        const SizedBox(width: 12),
                        Expanded(
                          child: Text(
                            labels[p]!,
                            style: TextStyle(fontWeight: FontWeight.w700, fontSize: 15, color: p == page ? c.onStrong : c.muted),
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            ),
          const Spacer(),
          // The browser sharing running, whatever the page: a click back to it.
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: WebShareBanner(onOpen: state.webShare.show, margin: EdgeInsets.zero),
          ),
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
                  style: FilledButton.styleFrom(minimumSize: const Size(0, 48), backgroundColor: c.section, foregroundColor: c.text),
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
                  Container(width: 10, height: 10, decoration: BoxDecoration(color: c.accent, shape: BoxShape.circle)),
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
