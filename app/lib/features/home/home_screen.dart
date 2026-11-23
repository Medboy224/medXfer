/// The home screen of « Version 4 » (plan J8, WP-8.1): the name, the tabs as pills, the
/// selection over the whole screen, and a bar floating above it: the basket, the transfer
/// running, or Connect.
library;

import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../app_state.dart';
import '../../l10n/app_localizations.dart';
import '../../library/selection.dart';
import '../../platform/app_exit.dart';
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
import '../explorer/gallery_toolbar.dart';
import '../explorer/selection_sheet.dart';
import '../history/history_tab.dart';
import '../settings/settings_screen.dart';
import '../transfer/transfer_screen.dart';
import '../trusted/trusted_screen.dart';
import '../webshare/web_share_screen.dart';

class HomeScreen extends StatelessWidget {
  const HomeScreen({super.key});

  @override
  Widget build(BuildContext context) => const DefaultTabController(length: 6, initialIndex: 1, child: _HomeFrame());

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
  static Widget _bar(BuildContext context) {
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
          child: Text(l.noDeviceConnected, style: TextStyle(color: c.muted, fontWeight: FontWeight.w600, fontSize: 13)),
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

/// The home screen's frame (plan J8, WP-8.1b): scrolling down folds the top line and the
/// toolbar away and hides the bar, for the content; scrolling up brings them back. The bar stays
/// while something is picked or a transfer runs.
/// What the back button leads to, once asked.
enum _Exit { toBack, quit }

class _HomeFrame extends StatefulWidget {
  const _HomeFrame();

  @override
  State<_HomeFrame> createState() => _HomeFrameState();
}

/// The frame shown, whose bars the lists' scrolling folds ([_FoldingPhysics]).
_HomeFrameState? _frame;

/// The finger's scroll goes first to the bars, then to the list: up, the top line and the
/// toolbar fold before the list moves; down, they come back before it does. Both at once, the
/// list's area grew as the bars folded while it scrolled too, and its content ran ahead of the
/// finger then caught up (real test of 22 November 2026). Vertical lists only; not while two
/// fingers pinch a grid.
class _FoldingPhysics extends ScrollPhysics {
  const _FoldingPhysics({super.parent});

  @override
  _FoldingPhysics applyTo(ScrollPhysics? ancestor) => _FoldingPhysics(parent: buildParent(ancestor));

  @override
  double applyPhysicsToUserOffset(ScrollMetrics position, double offset) {
    final frame = _frame;
    final left = frame == null || position.axis != Axis.vertical || explorerPinching.value ? offset : frame._consume(offset, position);
    return super.applyPhysicsToUserOffset(position, left);
  }
}

/// [_FoldingPhysics] for every list under the tabs.
class _FoldingBehavior extends MaterialScrollBehavior {
  const _FoldingBehavior();

  @override
  ScrollPhysics getScrollPhysics(BuildContext context) => _FoldingPhysics(parent: super.getScrollPhysics(context));
}

class _HomeFrameState extends State<_HomeFrame> with SingleTickerProviderStateMixin {
  bool _folded = false;

  // The top line and the toolbar fold with the finger, pixel for pixel: slowly with a slow
  // scroll, back as it scrolls the other way; the finger lifted, it ends its move by itself, to
  // the nearer of shown or folded (real tests of 22 November 2026: folded at once whatever the
  // scroll, then left half way). The top line only: the tabs and the gallery's toolbar always
  // show. 0 shown, 1 folded; its size follows without a rebuild ([SizeTransition]).
  late final AnimationController _fold = AnimationController(vsync: this)..addListener(_foldMoved);
  late final Animation<double> _shown = ReverseAnimation(_fold);
  final _topKey = GlobalKey();
  bool _dragging = false;

  // Back on the screen: what the device holds may have changed meanwhile (a photo taken in
  // another app, a file downloaded); the explorer reads it again (22 November 2026).
  late final _life = AppLifecycleListener(
    onResume: () {
      _away = false;
      _prepare();
      unawaited(AppScope.of(context, listen: false).state.explorer.refresh());
    },
    onHide: () {
      _away = true;
      _prepare();
    },
  );

  // The sharp thumbnails made ahead (23 November 2026), while the app is on screen, allowed to
  // read the media, and no transfer runs: the transfer keeps every core it can.
  bool _away = false;
  ({int height, int width})? _sharpSize;
  bool? _preparing;
  AppState? _watched;

  void _prepare() {
    final state = _watched;
    if (state == null || !mounted) return;
    final a = state.transfers.active;
    final size = _sharpSize;
    final on = !_away && size != null && state.library.supported && state.explorer.mediaAllowed == true && (a == null || a.stage == Stage.done);
    if (on == _preparing) return;
    _preparing = on;
    unawaited(state.library.prepareSharp(height: size?.height ?? 0, width: size?.width ?? 0, on: on).catchError((Object _) {}));
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _sharpSize = sharpSize(context);
    final state = AppScope.of(context, listen: false).state;
    if (!identical(state, _watched)) {
      _watched?.transfers.removeListener(_prepare);
      _watched?.explorer.removeListener(_prepare);
      _watched = state
        ..transfers.addListener(_prepare)
        ..explorer.addListener(_prepare);
    }
    _prepare();
  }

  @override
  void initState() {
    super.initState();
    _frame = this;
    _life; // listening from now
  }

  /// Android's back on the home screen (real test of 22 November 2026: in a folder of Files, it
  /// closed the app). In Files it goes up a folder, or from a category back to the folders;
  /// otherwise it asks before leaving.
  Future<void> _back() async {
    final state = AppScope.of(context, listen: false).state;
    final ex = state.explorer;
    if (DefaultTabController.of(context).index == 5 && state.library.supported) {
      if (ex.category != null) {
        ex.showCategory(null);
        return;
      }
      final parent = ex.parent;
      if (parent != null && ex.entries != null) {
        unawaited(ex.open(parent));
        return;
      }
    }
    await _askExit(state);
  }

  /// Quit, or keep in the background what runs: said plainly, the back button left the
  /// session open without a word (real test of 22 November 2026).
  Future<void> _askExit(AppState state) async {
    final running = state.keeper.running;
    final choice = await showDialog<_Exit>(
      context: context,
      builder: (context) {
        final l = AppLocalizations.of(context);
        final c = context.colors;
        if (running == null) {
          return AlertDialog(
            key: const Key('exit-dialog'),
            title: Text(l.exitTitle),
            actions: [
              TextButton(onPressed: () => Navigator.pop(context), child: Text(l.cancel)),
              FilledButton(key: const Key('exit-quit'), onPressed: () => Navigator.pop(context, _Exit.quit), child: Text(l.exitQuit)),
            ],
          );
        }
        return AlertDialog(
          key: const Key('exit-dialog'),
          title: Text(l.exitTitle),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(running.title, key: const Key('exit-running'), style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 15)),
              if (running.text.isNotEmpty) Text(running.text, style: TextStyle(color: c.muted, fontSize: 13)),
              const SizedBox(height: 16),
              FilledButton(
                key: const Key('exit-keep'),
                style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(48)),
                onPressed: () => Navigator.pop(context, _Exit.toBack),
                child: Text(l.exitKeep),
              ),
              Padding(
                padding: const EdgeInsets.fromLTRB(4, 4, 4, 12),
                child: Text(l.exitKeepHint, style: TextStyle(color: c.muted, fontSize: 12)),
              ),
              OutlinedButton(
                key: const Key('exit-quit'),
                style: OutlinedButton.styleFrom(minimumSize: const Size.fromHeight(48), foregroundColor: c.danger, side: BorderSide(color: c.danger)),
                onPressed: () => Navigator.pop(context, _Exit.quit),
                child: Text(l.exitQuitAll),
              ),
              Padding(
                padding: const EdgeInsets.fromLTRB(4, 4, 4, 0),
                child: Text(l.exitQuitAllHint, style: TextStyle(color: c.muted, fontSize: 12)),
              ),
            ],
          ),
          actions: [TextButton(onPressed: () => Navigator.pop(context), child: Text(l.cancel))],
        );
      },
    );
    switch (choice) {
      case _Exit.toBack:
        await appExit.toBack();
      case _Exit.quit:
        // The other device hears it end, rather than a session gone silent; then all stops.
        if (state.connection.session != null) {
          try {
            await state.connection.disconnect().timeout(const Duration(seconds: 2));
          } on Object catch (_) {
            // Gone already, or no answer: the core stops all the same.
          }
        }
        await state.shutdown(wait: const Duration(seconds: 2));
        await appExit.quit();
      case null:
        break;
    }
  }

  /// The bottom bar: hidden past 70 % of the fold, back under 30 %: a fold near half way does
  /// not make it blink.
  void _foldMoved() {
    final folded = _folded ? _fold.value > 0.3 : _fold.value > 0.7;
    if (folded != _folded) setState(() => _folded = folded);
  }

  /// The finger lifted (a drag ended, or the list went on by itself): the fold ends its move,
  /// in 0.12 s, to the nearer end.
  bool _scrolled(ScrollNotification n) {
    if (n.metrics.axis != Axis.vertical) return false;
    final drag = (n is ScrollUpdateNotification && n.dragDetails != null) || (n is OverscrollNotification && n.dragDetails != null);
    if (drag) {
      _dragging = true;
      return false;
    }
    final lifted = n is ScrollEndNotification || (n is ScrollUpdateNotification && n.dragDetails == null);
    if (_dragging && lifted) {
      _dragging = false;
      // It ends the way the finger last moved it: begun to come back, it comes back; folding,
      // it folds (to the nearer end, it came back then left again, 22 November 2026).
      final v = _fold.value;
      if (v > 0 && v < 1 && !explorerPinching.value) {
        unawaited(_fold.animateTo(_revealing ? 0 : 1, duration: const Duration(milliseconds: 120), curve: Curves.easeOut));
      }
      // Each gesture counts afresh: two slow scrolls do not add up to a long one.
      _down = 0;
      _recent.clear();
      _revealing = false;
    }
    return false;
  }

  @override
  void dispose() {
    if (_frame == this) _frame = null;
    _watched?.transfers.removeListener(_prepare);
    _watched?.explorer.removeListener(_prepare);
    _life.dispose();
    _fold.dispose();
    super.dispose();
  }

  /// The folding part's height, as laid out at full size.
  double get _foldable => (_topKey.currentContext?.findRenderObject() as RenderBox?)?.size.height ?? 0;

  /// How far the finger goes down, scrolling the list back, before the bars come back: folded,
  /// they leave more room, and came back at the least move (real test of 22 November 2026).
  ///
  /// The bars come back for a clear intent only: a quick move down ([revealAfter] at
  /// [revealSpeed] or more), or a long one ([revealSlowAfter]); a slow scroll back through the
  /// list leaves them folded (real tests of 22 November 2026).
  static const revealAfter = 120.0;
  static const revealSpeed = 700.0; // pixels a second, over the last 120 ms
  static const revealSlowAfter = 360.0;
  double _down = 0; // the finger's way down since it last went up, in pixels
  bool _revealing = false; // the finger's last move: bringing the bars back
  final _clock = Stopwatch()..start();
  final _recent = <(int, double)>[]; // the finger's last moves down: (ms, pixels)

  /// How fast the finger has gone down over the last 120 ms, in pixels a second.
  double _speedDown() {
    final now = _clock.elapsedMilliseconds;
    _recent.removeWhere((m) => now - m.$1 > 120);
    if (_recent.isEmpty) return 0;
    final span = math.max(16, now - _recent.first.$1);
    return _recent.fold(0.0, (a, m) => a + m.$2) * 1000 / span;
  }

  /// Takes what the bars use of a finger's move ([offset]: + down, the list toward its top);
  /// gives back the rest, for the list.
  double _consume(double offset, ScrollMetrics position) {
    final height = _foldable;
    if (height <= 0 || offset == 0) return offset;
    if (_fold.isAnimating) _fold.stop(); // the finger again: it takes the fold where it is
    _dragging = true; // a finger moves, even if the list does not: it ends the fold on lifting
    var waited = 0.0; // the part of a move down that goes to the list while the bars wait
    if (offset < 0) {
      _down = 0;
      _recent.clear();
      _revealing = false;
    } else if (position.pixels > position.minScrollExtent && _fold.value > 0) {
      // Down, the list not at its top, the bars folded: they wait for a clear intent.
      _recent.add((_clock.elapsedMilliseconds, offset));
      final before = _down;
      _down += offset;
      final quick = _down >= revealAfter && _speedDown() >= revealSpeed;
      final long = _down >= revealSlowAfter;
      if (!_revealing && !quick && !long) return offset;
      if (!_revealing) {
        // From here, the rest of this move goes to the bars.
        waited = math.max(0.0, math.min(offset, (quick ? revealAfter : revealSlowAfter) - before));
        offset -= waited;
        _revealing = true;
      }
    } else if (offset > 0) {
      _revealing = true; // at the top: everything comes back
    }
    final used = offset < 0 ? math.min(-offset, (1 - _fold.value) * height) : math.min(offset, _fold.value * height);
    if (used <= 0) return offset + waited;
    _fold.value = (_fold.value + (offset < 0 ? used : -used) / height).clamp(0.0, 1.0);
    return (offset < 0 ? offset + used : offset - used) + waited;
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final state = AppScope.of(context).state;
    final tabs = [
      (l.tabHistory, Icons.history_rounded),
      (l.tabApps, Icons.apps_rounded),
      (l.tabPhotos, Icons.image_outlined),
      (l.tabVideos, Icons.smart_display_outlined),
      (l.tabMusic, Icons.music_note_outlined),
      (l.tabFiles, Icons.folder_outlined),
    ];
    final motion = MediaQuery.of(context).disableAnimations ? Duration.zero : const Duration(milliseconds: 200);
    return PopScope(
      canPop: false,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) unawaited(_back());
      },
      child: Scaffold(
      // The bar floats: the grids run under it (their bottom padding leaves room).
      extendBody: true,
      body: SafeArea(
        bottom: false,
        child: ListenableBuilder(
          listenable: state.connection,
          builder: (context, child) => DropZone(
            enabled: state.connection.session != null,
            hint: l.dropHere,
            onPaths: (paths) => unawaited(AppScope.of(context, listen: false).state.transfers.send(paths)),
            child: child!,
          ),
          child: Column(
            children: [
              SizeTransition(
                sizeFactor: _shown,
                alignment: Alignment.bottomCenter, // folding up: its bottom stays last
                child: KeyedSubtree(key: _topKey, child: const _TopLine()),
              ),
              TabBar(
                isScrollable: true,
                padding: const EdgeInsets.fromLTRB(7, 2, 7, 4),
                labelPadding: const EdgeInsets.symmetric(horizontal: 15),
                indicatorPadding: const EdgeInsets.symmetric(horizontal: 3),
                tabs: [
                  for (final (label, icon) in tabs)
                    Tab(
                      height: 40,
                      child: Row(mainAxisSize: MainAxisSize.min, children: [Icon(icon, size: 16), const SizedBox(width: 6), Text(label)]),
                    ),
                ],
              ),
              const _CoreLine(),
              const _SessionBanner(),
              WebShareBanner(onOpen: () => HomeScreen._open(context, const WebShareScreen())),
              const GalleryToolbar(), // always shown: search, sort, layout at hand
              Expanded(
                child: NotificationListener<ScrollNotification>(
                  onNotification: _scrolled,
                  child: ScrollConfiguration(
                  behavior: const _FoldingBehavior(),
                  child: ValueListenableBuilder<bool>(
                    valueListenable: explorerPinching,
                    builder: (context, pinching, child) => TabBarView(
                    physics: pinching ? const NeverScrollableScrollPhysics() : null,
                    children: [
                      const HistoryTab(),
                      if (state.library.supported) ...[
                        const AppsTab(),
                        const MediaGridTab(kind: MediaKind.images),
                        const MediaGridTab(kind: MediaKind.video),
                        const MusicTab(),
                        FilesTab(onSystemPicker: () => unawaited(HomeScreen._sendPicked(context))),
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
                ),
                ),
              ),
            ],
          ),
        ),
      ),
      bottomNavigationBar: ListenableBuilder(
        listenable: Listenable.merge([state.connection, state.transfers, state.selection]),
        builder: (context, _) {
          final a = state.transfers.active;
          final keep = !state.selection.isEmpty || (a != null && a.stage != Stage.done);
          final hidden = _folded && !keep;
          return SafeArea(
            child: AnimatedSlide(
              offset: hidden ? const Offset(0, 1.6) : Offset.zero,
              duration: motion,
              curve: Curves.easeOutCubic,
              child: AnimatedOpacity(
                opacity: hidden ? 0 : 1,
                duration: motion,
                child: IgnorePointer(
                  ignoring: hidden,
                  child: Padding(
                    padding: const EdgeInsets.fromLTRB(10, 0, 10, 10),
                    child: DecoratedBox(
                      key: const Key('bottom-bar'),
                      decoration: BoxDecoration(
                        color: c.section,
                        borderRadius: BorderRadius.circular(18),
                        boxShadow: [BoxShadow(color: c.shadow, blurRadius: 24, offset: const Offset(0, 8))],
                      ),
                      child: Padding(padding: const EdgeInsets.all(4), child: HomeScreen._bar(context)),
                    ),
                  ),
                ),
              ),
            ),
          );
        },
      ),
    ),
    );
  }
}

/// The name, the QR reader and the menu: the line that folds away.
class _TopLine extends StatelessWidget {
  const _TopLine();

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return SizedBox(
      height: 48,
      child: Row(
        children: [
          const SizedBox(width: 14),
          Expanded(
            child: Text(l.appTitle, style: titleStyle(19, weight: FontWeight.w900, color: c.text).copyWith(fontStyle: FontStyle.italic)),
          ),
          IconButton(tooltip: l.scanQr, icon: const Icon(Icons.qr_code_scanner_rounded, size: 21), onPressed: () => HomeScreen._joinByQr(context)),
          PopupMenuButton<String>(
            key: const Key('home-menu'),
            tooltip: l.menu,
            icon: const Icon(Icons.more_vert_rounded, size: 21),
            offset: const Offset(0, 44),
            onSelected: (v) => switch (v) {
              'trusted' => HomeScreen._open(context, const TrustedScreen()),
              'browser' => HomeScreen._open(context, const WebShareScreen()),
              'dev' => DevPanel.open(Navigator.of(context)),
              _ => HomeScreen._open(context, const SettingsScreen()),
            },
            itemBuilder: (_) => [
              HomeScreen._menuItem(context, 'browser', Icons.public_rounded, l.webShareTitle, key: const Key('menu-browser')),
              HomeScreen._menuItem(context, 'trusted', Icons.verified_user_outlined, l.trustedDevices),
              HomeScreen._menuItem(context, 'settings', Icons.tune_rounded, l.settings),
              if (AppScope.of(context, listen: false).settings.devMode) HomeScreen._menuItem(context, 'dev', Icons.code_rounded, l.devPanel),
            ],
          ),
          const SizedBox(width: 2),
        ],
      ),
    );
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
        Text(label, style: TextStyle(color: c.muted, fontSize: 12)),
        Text(name, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 14)),
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
        // The count over the size, each on its line (real test of 17 November: « 5.9 / MB »);
        // touched, what is picked across every tab, to see and take out (22 November).
        Expanded(
          child: Semantics(
            label: l.basketLine(sel.count, formatBytes(context, sel.bytes)),
            hint: l.selectionTitle,
            button: true,
            excludeSemantics: true,
            child: InkWell(
              key: const Key('basket-open'),
              borderRadius: BorderRadius.circular(12),
              onTap: () => unawaited(showSelectionSheet(context)),
              child: Padding(
                padding: const EdgeInsets.symmetric(vertical: 4),
                child: Row(
                  children: [
                    Flexible(
                      child: Column(
              key: const Key('basket-line'),
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(l.itemsCount(sel.count), maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 14, fontFeatures: tabular)),
                Text(formatBytes(context, sel.bytes), maxLines: 1, style: TextStyle(color: c.muted, fontSize: 12, fontFeatures: tabular)),
                      ],
                    ),
                    ),
                    Icon(Icons.expand_less_rounded, size: 20, color: c.muted),
                  ],
                ),
              ),
            ),
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
        FilledButton.icon(
          key: const Key('basket-send'),
          onPressed: () =>
              connected ? unawaited(_send(context)) : unawaited(Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => const ConnectScreen()))),
          icon: Icon(connected ? Icons.north_east_rounded : Icons.link_rounded),
          iconAlignment: connected ? IconAlignment.end : IconAlignment.start,
          label: Text(connected ? l.sendSelection : l.connect, maxLines: 1),
        ),
      ],
    );
  }
}
