/// The tabs of the explorer (WP-B.9, §1.2 of the J6b plan): Apps and Photos in grids, Videos,
/// Music, Files. A tap picks or unpicks; the basket of the home screen shows the selection.
/// Lists are lazy and the images decoded at the size shown (UI-1).
library;

import 'dart:async';
import 'dart:math' as math;
import 'dart:ui' as ui;

import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../../app.dart';
import '../../l10n/app_localizations.dart';
import '../../library/explorer.dart';
import '../../platform/library.dart';
import '../../theme.dart';
import '../../widgets/format.dart';
import '../../widgets/v4.dart';

/// A thumbnail or an icon from the explorer's cache, decoded at its displayed size.
class LibraryImage extends StatefulWidget {
  const LibraryImage({super.key, required this.cacheKey, required this.load, this.fit = BoxFit.cover, this.placeholder, this.decodePx});

  final String cacheKey;

  /// Loads the image at the size it is shown, in pixels: sharp, never stretched.
  final Future<Uint8List?> Function(int px) load;
  final BoxFit fit;

  /// Shown until the image comes, and instead of it if there is none: a plain tile otherwise.
  final Widget? placeholder;

  /// The size it is loaded and decoded at, in pixels, rather than its own: the photos' grids,
  /// one size for all their numbers of columns, so that a pinch decodes nothing (decoded again
  /// at each grid's size, the tiles of the next grid stuttered the gesture, 18 November 2026).
  final int? decodePx;

  @override
  State<LibraryImage> createState() => _LibraryImageState();
}

class _LibraryImageState extends State<LibraryImage> {
  Uint8List? _bytes;
  String? _asked;
  late Explorer _explorer;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _explorer = AppScope.of(context, listen: false).state.explorer;
  }

  void _fetch(int px) {
    final key = widget.cacheKey;
    if (_asked == key) return;
    _asked = key;
    _bytes = null;
    final explorer = _explorer;
    final cached = explorer.cached(key);
    if (cached != null) {
      _bytes = cached; // there at once, without a frame of placeholder
      return;
    }
    explorer.image(key, () => widget.load(px)).then((bytes) {
      if (mounted && _asked == key && bytes != null) setState(() => _bytes = bytes);
    });
  }

  @override
  void dispose() {
    // Scrolled away before its turn: not loaded at all.
    if (_bytes == null && _asked != null) _explorer.forget(_asked!);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, box) {
        final dpr = MediaQuery.devicePixelRatioOf(context);
        final px = widget.decodePx ?? (box.biggest.shortestSide * dpr).round().clamp(32, 1024);
        _fetch(px);
        final bytes = _bytes;
        if (bytes == null) return widget.placeholder ?? ColoredBox(color: context.colors.chip);
        return Image.memory(bytes, fit: widget.fit, cacheWidth: px, gaplessPlayback: true, filterQuality: FilterQuality.medium);
      },
    );
  }
}

class _Message extends StatelessWidget {
  const _Message({required this.text, this.action});

  final String text;
  final Widget? action;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              text,
              textAlign: TextAlign.center,
              style: TextStyle(color: context.colors.muted),
            ),
            if (action != null) ...[const SizedBox(height: 12), action!],
          ],
        ),
      ),
    );
  }
}

class AppsTab extends StatefulWidget {
  const AppsTab({super.key});

  @override
  State<AppsTab> createState() => _AppsTabState();
}

class _AppsTabState extends State<AppsTab> with AutomaticKeepAliveClientMixin {
  @override
  bool get wantKeepAlive => true;

  @override
  void initState() {
    super.initState();
    unawaited(AppScope.of(context, listen: false).state.explorer.loadApps());
  }

  @override
  Widget build(BuildContext context) {
    super.build(context);
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    return ListenableBuilder(
      listenable: Listenable.merge([state.explorer, state.selection]),
      builder: (context, _) {
        final apps = state.explorer.apps;
        if (apps == null) return const Center(child: CircularProgressIndicator());
        if (apps.isEmpty) return _Message(text: l.nothingHere);
        return GridView.builder(
          key: const Key('apps-grid'),
          padding: EdgeInsets.fromLTRB(6, 4, 6, bottomRoom(context)),
          gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(maxCrossAxisExtent: 100, mainAxisExtent: 124, mainAxisSpacing: 4),
          itemCount: apps.length,
          itemBuilder: (context, i) {
            final a = apps[i];
            final pick = Explorer.pickApp(a);
            final on = state.selection.contains(pick.path);
            return Material(
              color: on ? context.colors.soft : Colors.transparent,
              borderRadius: BorderRadius.circular(16),
              child: InkWell(
              key: ValueKey('app-${a.package}'),
              borderRadius: BorderRadius.circular(16),
              onTap: () {
                state.selection.toggle(pick);
                // Installed in parts (Play's splits): said when picked, in words.
                if (a.split && state.selection.contains(pick.path)) {
                  ScaffoldMessenger.of(context)
                    ..clearSnackBars()
                    ..showSnackBar(SnackBar(content: Text(l.splitApp)));
                }
              },
              child: Padding(
                padding: const EdgeInsets.fromLTRB(2, 12, 2, 6),
                child: Column(
                  children: [
                    Stack(
                      clipBehavior: Clip.none,
                      children: [
                        SizedBox.square(
                          dimension: 50,
                          child: LibraryImage(cacheKey: 'app:${a.package}', fit: BoxFit.contain, load: (px) => state.library.appIcon(a.package, px)),
                        ),
                        // Shown once picked only: a grid of empty boxes hid the icons.
                        if (on) Positioned(right: -6, top: -6, child: PickBox(on: on, size: 20)),
                      ],
                    ),
                    const SizedBox(height: 6),
                    Text(
                      a.label,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w600),
                    ),
                    Text(formatBytes(context, a.size), style: TextStyle(color: context.colors.muted, fontSize: 11)),
                  ],
                ),
              ),
            ),
            );
          },
        );
      },
    );
  }
}

/// Asks for the media once, if needed; then shows the tab.
class _MediaGate extends StatefulWidget {
  const _MediaGate({required this.child});

  final Widget child;

  @override
  State<_MediaGate> createState() => _MediaGateState();
}

class _MediaGateState extends State<_MediaGate> {
  @override
  void initState() {
    super.initState();
    final explorer = AppScope.of(context, listen: false).state.explorer;
    if (explorer.mediaAllowed == null) unawaited(explorer.checkMedia());
  }

  @override
  Widget build(BuildContext context) {
    final explorer = AppScope.of(context).state.explorer;
    final l = AppLocalizations.of(context);
    return ListenableBuilder(
      listenable: explorer,
      builder: (context, _) => switch (explorer.mediaAllowed) {
        null => const Center(child: CircularProgressIndicator()),
        false => _Message(
          text: l.mediaHint,
          action: FilledButton(key: const Key('media-allow'), onPressed: () => unawaited(explorer.askMedia()), child: Text(l.mediaAllow)),
        ),
        true => widget.child,
      },
    );
  }
}

/// Photos or videos, newest first, in a grid; filtered by folder.
class MediaGridTab extends StatelessWidget {
  const MediaGridTab({super.key, required this.kind});

  final MediaKind kind;

  @override
  Widget build(BuildContext context) => _MediaGate(child: _MediaGrid(kind: kind));
}

class _MediaGrid extends StatefulWidget {
  const _MediaGrid({required this.kind});

  final MediaKind kind;

  @override
  State<_MediaGrid> createState() => _MediaGridState();
}

/// The size the photos' grids decode their thumbnails at, whatever their number of columns:
/// the tile of the fewest square columns (3), sharp there, and shared by all.
int gridDecodePx(BuildContext context) {
  final width = MediaQuery.sizeOf(context).width;
  return ((width - 2 * 2) / 3 * MediaQuery.devicePixelRatioOf(context)).round().clamp(32, 1024);
}

/// A grid's scrolling, still while two fingers pinch it ([explorerPinching]): the pinch is
/// painted over it, and its scroll moving under the fingers folded the top bar (real test of
/// 22 November 2026). Read at each move: nothing is rebuilt when the pinch starts.
class _PinchablePhysics extends ScrollPhysics {
  const _PinchablePhysics({super.parent});

  @override
  _PinchablePhysics applyTo(ScrollPhysics? ancestor) => _PinchablePhysics(parent: buildParent(ancestor));

  @override
  double applyPhysicsToUserOffset(ScrollMetrics position, double offset) =>
      explorerPinching.value ? 0 : super.applyPhysicsToUserOffset(position, offset);

  @override
  Simulation? createBallisticSimulation(ScrollMetrics position, double velocity) =>
      explorerPinching.value ? null : super.createBallisticSimulation(position, velocity);
}

/// While two fingers pinch a grid: the tabs do not page under them.
final explorerPinching = ValueNotifier<bool>(false);

/// From the start of a pinch to the end of its settling, what is drawn over the photos (the
/// videos' durations, the boxes, the days' names) fades away, then back, as in a phone's
/// gallery (video of the Galaxy A57's, 18 November 2026: the grids zoom bare). One animation for
/// all of it, given down the grid: painted at each frame, nothing rebuilt (one animation per
/// box and per duration, in four grids, stuttered the gesture).
class _DecorScope extends InheritedWidget {
  const _DecorScope({required this.shown, required super.child});

  final Animation<double> shown;

  static Animation<double> of(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<_DecorScope>()?.shown ?? kAlwaysCompleteAnimation;

  @override
  bool updateShouldNotify(_DecorScope old) => old.shown != shown;
}

/// What is drawn over the photos: faded out while a pinch runs ([_DecorScope]), to [dim] rather
/// than to nothing for what must stay readable.
class _Decor extends StatelessWidget {
  const _Decor({required this.child, this.dim = 0});

  final Widget child;
  final double dim;

  @override
  Widget build(BuildContext context) {
    final shown = _DecorScope.of(context);
    return FadeTransition(opacity: dim == 0 ? shown : Tween<double>(begin: dim, end: 1).animate(shown), child: child);
  }
}

class _MediaGridState extends State<_MediaGrid> with AutomaticKeepAliveClientMixin, TickerProviderStateMixin {
  String? _album;
  ScrollController _scroll = ScrollController();

  // The pinch (plan J8, WP-8.1b, real test of 17 November): the grid follows the fingers,
  // around the point between them; past a step it takes the next number of columns, the tile
  // under the fingers kept there; scrolling waits while two fingers are down.
  final _points = <int, Offset>{};
  final _pressedAt = <int, Offset>{}; // where each finger came down: a scroll under way is no pinch
  final _heardAt = <int, Duration>{}; // each finger's last event: one silent too long was lost
  double? _startSpread;
  double _carry = 0; // levels already moved when the fingers came down: + fewer columns
  final _scaleN = ValueNotifier<double>(1);
  double get _scale => _scaleN.value;
  set _scale(double v) => _scaleN.value = v;
  Offset _focal = Offset.zero;
  Size _box = Size.zero;
  late final AnimationController _settle = AnimationController(vsync: this, duration: const Duration(milliseconds: 240))
    ..addListener(() => _scale = _settleFrom + (_settleGoal - _settleFrom) * Curves.easeOutCubic.transform(_settle.value))
    ..addStatusListener((status) {
      if (status == AnimationStatus.completed) _settled();
    });
  double _settleFrom = 1;
  double _settleGoal = 1;

  // What is over the photos: 1 shown, 0 gone during a pinch ([_DecorScope]).
  late final AnimationController _decor = AnimationController(
    vsync: this,
    value: 1,
    duration: const Duration(milliseconds: 200),
    reverseDuration: const Duration(milliseconds: 150),
  );

  // As in a phone's gallery (its video, 18 November): between two numbers of columns, the next
  // grid fades in over the current one, both at the scale of the fingers and the tile under them
  // at the same place. While the fingers are down, the grids are painted, not built: one surface
  // draws the thumbnails already decoded at their places in each grid ([_PinchPainter]); the
  // real grid is built once, behind it, when the gesture is over. Built during the gesture, the
  // grids took 50 ms each and the photos jumped (18 November 2026).
  int? _target;
  final _pinchN = ValueNotifier<_Pinch?>(null);
  final _painted = ChangeNotifier(); // a thumbnail decoded meanwhile: painted again
  int _ending = 0; // the end of a pinch under way; a new pinch changes it, which cancels that end

  // The real grid given again as the same widget while nothing it shows changed: not rebuilt.
  (Object, Widget)? _view;

  // The layout last shown, to keep the same tile in place when it changes.
  List<int> _groups = const [];
  bool _headers = false;
  int _shownColumns = 0;
  bool _shownList = false;

  static const _headerHeight = 52.0;
  static const _rowHeight = 62.0;
  static const _gap = 2.0;

  @override
  bool get wantKeepAlive => true;

  @override
  void initState() {
    super.initState();
    final explorer = AppScope.of(context, listen: false).state.explorer;
    if (explorer.media(widget.kind).items.isEmpty) unawaited(explorer.more(widget.kind));
  }

  @override
  void dispose() {
    _settle.dispose();
    _decor.dispose();
    _scaleN.dispose();
    _scroll.dispose();
    _pinchN.value?.release();
    _pinchN.dispose();
    _painted.dispose();
    super.dispose();
  }

  /// How far the fade toward [_target] is: 0 at the current size, 1 at the target's.
  double get _progress {
    final t = _target;
    if (t == null) return 0;
    final k = _shownColumns / t;
    if (k > 1 ? _scale <= 1 : _scale >= 1) return 0;
    return (math.log(_scale) / math.log(k)).clamp(0.0, 1.0).toDouble();
  }

  /// The painted pinch: every number of columns placed under the fingers (each one's scroll
  /// puts the tile under them at the same place), and the thumbnails it may show, already
  /// decoded at the grids' size ([gridDecodePx]): taken from the image cache, none decoded.
  _Pinch? _startPinch(BuildContext context, List<MediaEntry> items, List<(DateTime, List<MediaEntry>)> days) {
    if (!_scroll.hasClients || _box.isEmpty) return null;
    final state = AppScope.of(context, listen: false).state;
    final c = _shownColumns;
    final index = _at(_scroll.offset + _focal.dy, c, false, x: _focal.dx);
    final within = _scroll.offset + _focal.dy - _top(index, c, false);
    final offsets = <int, double>{c: _scroll.offset};
    for (var t = Explorer.minColumns; t <= Explorer.maxColumns; t++) {
      if (t != c) offsets[t] = (_top(index, t, false) + within * c / t - _focal.dy).clamp(0.0, double.infinity).toDouble();
    }
    // The items each grid may show: its screen, and as much again, the zoom revealing more.
    final h = _box.height;
    final wanted = <int>{};
    for (final e in offsets.entries) {
      final from = _at(math.max(0, e.value - h * 0.6), e.key, false);
      final to = math.min(items.length - 1, _at(e.value + h * 1.6, e.key, false) + e.key);
      for (var i = from; i <= to; i++) {
        wanted.add(i);
      }
    }
    final px = gridDecodePx(context);
    final config = createLocalImageConfiguration(context);
    final colors = context.colors;
    final pinch = _Pinch(
      columns: c,
      offsets: offsets,
      groups: _groups,
      headers: _headers,
      box: _box,
      focal: _focal,
      background: colors.background,
      chip: colors.chip,
      strong: colors.strong,
      days: [
        for (final d in days)
          TextPainter(text: TextSpan(text: _dayName(context, d.$1), style: titleStyle(14).copyWith(color: colors.text.withValues(alpha: 0.35))), textDirection: Directionality.of(context))..layout(),
      ],
    );
    for (final i in wanted) {
      final m = items[i];
      if (state.selection.contains(Explorer.pickMedia(m).path)) pinch.picked.add(i);
      final bytes = state.explorer.cached('${widget.kind.name}:${m.id}');
      if (bytes == null) continue;
      // The same image as the grid's tile (LibraryImage: Image.memory at cacheWidth px).
      final stream = ResizeImage(MemoryImage(bytes), width: px).resolve(config);
      final listener = ImageStreamListener((info, sync) {
        pinch.images[i] = info.image;
        if (!sync) _painted.notifyListeners(); // ignore: invalid_use_of_protected_member, invalid_use_of_visible_for_testing_member
      });
      stream.addListener(listener);
      pinch.streams.add((stream, listener));
    }
    return pinch;
  }

  /// The gesture settled at [_shownColumns]: the real grid built there, behind the painted one,
  /// which goes a frame after; then the other screens are told, once.
  void _endPinch(Explorer ex) {
    final pinch = _pinchN.value;
    if (pinch == null) return;
    final ending = ++_ending;
    final c = _shownColumns;
    final changed = c != pinch.built;
    if (changed) {
      final old = _scroll;
      _scroll = ScrollController(initialScrollOffset: pinch.offsets[c] ?? 0);
      pinch.built = c;
      ex.setLayout(widget.kind, columns: c, asList: false, quiet: true);
      setState(() {});
      WidgetsBinding.instance.addPostFrameCallback((_) => old.dispose());
    } else if (_scroll.hasClients) {
      _scroll.jumpTo(pinch.offsets[c]!); // two fingers may have scrolled it, under the painting
    }
    // Two frames, each asked for: nothing else may move on screen to bring them.
    final binding = WidgetsBinding.instance;
    binding.addPostFrameCallback((_) {
      if (!mounted) return;
      if (changed) ex.layoutSettled();
      binding.addPostFrameCallback((_) {
        // Two fingers down again meanwhile: the painting stays for the new pinch.
        if (!mounted || _pinchN.value != pinch || ending != _ending) return;
        _pinchN.value = null;
        pinch.release();
        unawaited(_decor.forward()); // what is over the photos comes back
      });
      binding.scheduleFrame();
    });
    binding.scheduleFrame();
  }

  void _settled() {
    final t = _target;
    if (t != null && _settleGoal != 1) _shownColumns = t;
    _target = null;
    _scale = 1;
    _endPinch(AppScope.of(context, listen: false).state.explorer);
  }

  /// The top of item [index] in a layout: headers, rows of square tiles, or list rows.
  double _top(int index, int columns, bool asList) {
    if (asList) return index * _rowHeight;
    final stride = (_box.width - (columns - 1) * _gap) / columns + _gap;
    var y = 0.0;
    var i = index;
    for (final n in _groups) {
      if (_headers) y += _headerHeight;
      if (i < n) return y + (i ~/ columns) * stride;
      i -= n;
      y += ((n + columns - 1) ~/ columns) * stride - _gap;
    }
    return y;
  }

  /// The item at height [y] of a layout, and at [x] across (the column under the fingers).
  int _at(double y, int columns, bool asList, {double x = 0}) {
    final count = _groups.fold(0, (a, b) => a + b);
    if (count == 0) return 0;
    if (asList) return (y / _rowHeight).floor().clamp(0, count - 1);
    final stride = (_box.width - (columns - 1) * _gap) / columns + _gap;
    var top = 0.0;
    var first = 0;
    for (final n in _groups) {
      if (_headers) top += _headerHeight;
      final height = ((n + columns - 1) ~/ columns) * stride - _gap;
      final column = (x / stride).floor().clamp(0, columns - 1);
      if (y < top + height) return (first + ((y - top).clamp(0, height) ~/ stride) * columns + column).clamp(first, first + n - 1);
      top += height;
      first += n;
    }
    return count - 1;
  }

  /// A new layout: the item at [anchor] (from the top of the view) stays at that height.
  void _keep(int fromColumns, bool fromList, int toColumns, bool toList, double anchor) {
    if (!_scroll.hasClients || _box.width <= 0) return;
    final index = _at(_scroll.offset + anchor, fromColumns, fromList);
    final within = _scroll.offset + anchor - _top(index, fromColumns, fromList);
    final ratio = toList || fromList ? 0.0 : fromColumns / toColumns;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_scroll.hasClients) return;
      final y = _top(index, toColumns, toList) + within * ratio - anchor;
      _scroll.jumpTo(y.clamp(_scroll.position.minScrollExtent, _scroll.position.maxScrollExtent));
    });
  }

  double? _spread() {
    if (_points.length < 2) return null;
    final p = _points.values.take(2).toList();
    return (p[0] - p[1]).distance;
  }

  Offset _between() {
    final p = _points.values.take(2).toList();
    return (p[0] + p[1]) / 2;
  }

  /// Where the pinch stands, in levels from the grid shown: + toward fewer columns.
  double get _position {
    final t = _target;
    if (t == null) return 0;
    return t < _shownColumns ? _progress : -_progress;
  }

  void _down(PointerDownEvent e, Explorer ex, List<MediaEntry> items, List<(DateTime, List<MediaEntry>)> days) {
    // A finger whose lifting never came (silent more than 1.5 s, no pinch under way) is no
    // longer counted: with it, a real pinch had three fingers and was taken for a scroll (real
    // test of 22 November 2026).
    if (_startSpread == null) {
      final lost = [
        for (final p in _points.keys)
          if (e.timeStamp - (_heardAt[p] ?? e.timeStamp) > const Duration(milliseconds: 1500)) p,
      ];
      for (final p in lost) {
        _points.remove(p);
        _pressedAt.remove(p);
        _heardAt.remove(p);
      }
      if (lost.isNotEmpty) debugPrint('pinch: ${lost.length} lost finger(s) dropped');
    }
    _points[e.pointer] = e.localPosition;
    _pressedAt[e.pointer] = e.localPosition;
    _heardAt[e.pointer] = e.timeStamp;
    if (_points.length != 2) {
      if (_points.length > 2) debugPrint('pinch: ${_points.length} fingers down');
      return;
    }
    // The first finger already scrolling: a second one (another finger, the palm) is no pinch,
    // the scroll goes on (real test of 22 November 2026).
    final first = _points.keys.firstWhere((p) => p != e.pointer);
    if ((_points[first]! - _pressedAt[first]!).distance > scrollingAfter && _startSpread == null) {
      debugPrint('pinch: a scroll under way, not a pinch');
      return;
    }
    if (!ex.asList(widget.kind) && _pinchN.value == null) {
      _settle.stop();
      _startSpread = _spread();
      _carry = 0;
      _scale = 1;
      _target = null;
      _focal = _between();
      final pinch = _startPinch(context, items, days);
      if (pinch == null) return;
      explorerPinching.value = true;
      _decor.value = 0; // covered by the painting: gone at once
      _pinchN.value = pinch; // the painting over the real grid; nothing else built
    } else if (_pinchN.value != null) {
      // Fingers down again while it settles or ends, a quarter of a second: the gesture goes on
      // from where it is, on the same painting, whose end is cancelled (chained pinches did
      // nothing, the painting taken away under the fingers, real test of 22 November 2026).
      _ending++;
      _settle.stop();
      _startSpread = _spread();
      _carry = _position;
      explorerPinching.value = true;
    }
  }

  /// How far a finger moves before a second one is no longer a pinch but a touch on a scroll.
  static const scrollingAfter = 60.0;

  /// The fingers' gesture in levels: every step between two numbers of columns asks for the same
  /// spread, the fingers' distance (plus [_zoomMargin]) changing by [_levelRatio]. Followed as a
  /// zoom of the grid, the steps toward fewer columns came faster than the others, and a small
  /// pinch went two levels (real tests of 22 November 2026).
  static const _levelRatio = 1.5;
  static const _zoomMargin = 80.0; // pixels added to both spreads: close fingers are not twitchy
  static double _levelsOf(double start, double now) => math.log((now + _zoomMargin) / (start + _zoomMargin)) / math.log(_levelRatio);

  void _move(PointerMoveEvent e, Explorer ex) {
    if (!_points.containsKey(e.pointer)) return;
    _points[e.pointer] = e.localPosition;
    _heardAt[e.pointer] = e.timeStamp;
    final start = _startSpread, now = _spread();
    if (start == null || now == null || start <= 0 || _pinchN.value == null) return;
    var c = _shownColumns;
    var pos = _carry + _levelsOf(start, now);
    // Each whole level passed: that grid becomes the current one, the gesture goes on from it.
    while (pos >= 1 && c - 1 >= Explorer.minColumns) {
      c--;
      pos -= 1;
      _carry -= 1;
    }
    while (pos <= -1 && c + 1 <= Explorer.maxColumns) {
      c++;
      pos += 1;
      _carry += 1;
    }
    _shownColumns = c;
    final t = pos > 0 ? c - 1 : (pos < 0 ? c + 1 : null);
    if (t == null || t < Explorer.minColumns || t > Explorer.maxColumns) {
      _target = null;
      _scale = 1; // at 2 or 5 columns, nothing further: the view itself never zooms
      return;
    }
    _target = t;
    _scale = math.pow(c / t, pos.abs()).toDouble(); // the painting follows; nothing is built
  }

  void _up(PointerEvent e) {
    _points.remove(e.pointer);
    _pressedAt.remove(e.pointer);
    _heardAt.remove(e.pointer);
    // The list stays still until every finger of the pinch is up: the one left down scrolled
    // it, and folded the top bar (real test of 22 November 2026).
    if (_points.isEmpty) explorerPinching.value = false;
    if (_points.length >= 2 || _startSpread == null) return;
    _startSpread = null;
    // Past half the fade, on to the next grid; else back to the current one.
    final t = _target;
    _settleFrom = _scale;
    _settleGoal = t != null && _progress >= 0.5 ? _shownColumns / t : 1;
    if (MediaQuery.of(context).disableAnimations) {
      _scale = _settleGoal;
      _settled();
    } else {
      unawaited(_settle.forward(from: 0));
    }
  }

  @override
  Widget build(BuildContext context) {
    super.build(context);
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return ListenableBuilder(
      listenable: Listenable.merge([state.explorer, state.selection]),
      builder: (context, _) {
        final list = state.explorer.media(widget.kind);
        if (list.items.isEmpty) {
          return list.loading || !list.done ? const Center(child: CircularProgressIndicator()) : _Message(text: list.error ?? l.nothingHere);
        }
        final albums = <String>{for (final m in list.items) m.album}.where((a) => a.isNotEmpty).toList();
        final items = _album == null ? list.items : list.items.where((m) => m.album == _album).toList();
        final ex = state.explorer;
        final asList = ex.asList(widget.kind);
        final columns = ex.columns(widget.kind);
        // By day while sorted by date (photos and videos); one block for a name or a size.
        final byDay = ex.order(widget.kind).sort == MediaSort.date;
        final days = byDay ? _days(items) : [(DateTime(0), items)];
        // Changed from the menu: the tile at the top of the view stays at the top. (A pinch
        // keeps its own anchor, under the fingers, before this build.)
        if (_shownColumns != 0 && (_shownColumns != columns || _shownList != asList) && _startSpread == null) {
          _keep(_shownColumns, _shownList, columns, asList, 0);
        }
        _groups = asList ? [items.length] : [for (final d in days) d.$2.length];
        _headers = byDay && !asList;
        _shownColumns = columns;
        _shownList = asList;
        return Column(
          children: [
            if (albums.length > 1)
              SizedBox(
                height: 56,
                child: ListView(
                  scrollDirection: Axis.horizontal,
                  padding: const EdgeInsets.fromLTRB(12, 0, 12, 12),
                  children: [
                    for (final a in [null, ...albums])
                      Padding(
                        padding: const EdgeInsets.only(right: 6),
                        child: ChoicePill(label: a ?? l.allAlbums, on: _album == a, onPressed: () => setState(() => _album = a)),
                      ),
                  ],
                ),
              ),
            Expanded(
              child: NotificationListener<ScrollNotification>(
                // The next page before the end is reached.
                onNotification: (n) {
                  if (n.metrics.extentAfter < 1500) unawaited(state.explorer.more(widget.kind));
                  return false;
                },
                child: LayoutBuilder(
                  builder: (context, box) {
                    _box = box.biggest;
                    Widget view(ScrollController controller, int cols, Key key) => CustomScrollView(
                      key: key,
                      controller: controller,
                      physics: const _PinchablePhysics(),
                      slivers: [
                        if (asList)
                          SliverPadding(
                            padding: const EdgeInsets.symmetric(horizontal: 8),
                            sliver: SliverFixedExtentList.builder(
                              itemExtent: _rowHeight,
                              itemCount: items.length,
                              itemBuilder: (context, i) => _MediaRow(kind: widget.kind, entry: items[i]),
                            ),
                          )
                        else
                          for (final day in days) ...[
                            if (byDay) SliverToBoxAdapter(child: SizedBox(height: _headerHeight, child: _DayHeader(day: day.$1, items: day.$2))),
                            _grid(day.$2, cols),
                          ],
                        SliverToBoxAdapter(child: SizedBox(height: bottomRoom(context))),
                      ],
                    );
                    // The same widget while nothing it shows changed: not rebuilt.
                    final shows = (list.items, _album, asList, byDay, ex.order(widget.kind), box.biggest, _scroll, columns);
                    final hit = _view;
                    // A new scroll (the grid a pinch settled on) is a new grid: an old one kept its
                    // offset, not the one under the fingers.
                    final grid = hit != null && hit.$1 == shows
                        ? hit.$2
                        : KeyedSubtree(key: ObjectKey(_scroll), child: view(_scroll, columns, ValueKey('media-${widget.kind.name}')));
                    _view = (shows, grid);
                    return _DecorScope(
                      shown: _decor,
                      child: Listener(
                        onPointerDown: (e) => _down(e, ex, items, days),
                        onPointerMove: (e) => _move(e, ex),
                        onPointerUp: _up,
                        onPointerCancel: _up,
                        child: ClipRect(
                          child: Stack(
                            fit: StackFit.expand,
                            children: [
                              grid,
                              // While two fingers pinch: the grids painted over the real one.
                              ValueListenableBuilder<_Pinch?>(
                                valueListenable: _pinchN,
                                builder: (context, pinch, _) => pinch == null
                                    ? const SizedBox.shrink()
                                    : IgnorePointer(
                                        child: CustomPaint(
                                          key: const ValueKey('pinch-painting'),
                                          size: Size.infinite,
                                          painter: _PinchPainter(pinch, this),
                                        ),
                                      ),
                              ),
                            ],
                          ),
                        ),
                      ),
                    );
                  },
                ),
              ),
            ),
            if (list.loading) LinearProgressIndicator(minHeight: 2, color: c.muted),
          ],
        );
      },
    );
  }

  Widget _grid(List<MediaEntry> items, int columns) => SliverGrid(
    gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(crossAxisCount: columns, mainAxisSpacing: _gap, crossAxisSpacing: _gap),
    delegate: SliverChildBuilderDelegate((context, i) => _MediaTile(kind: widget.kind, entry: items[i], columns: columns), childCount: items.length),
  );

  /// The media by day, in the order shown: what « Tout ce jour » picks.
  static List<(DateTime, List<MediaEntry>)> _days(List<MediaEntry> items) {
    final out = <(DateTime, List<MediaEntry>)>[];
    for (final m in items) {
      final t = DateTime.fromMillisecondsSinceEpoch(m.modified * 1000);
      final day = DateTime(t.year, t.month, t.day);
      if (out.isEmpty || out.last.$1 != day) out.add((day, <MediaEntry>[]));
      out.last.$2.add(m);
    }
    return out;
  }
}

/// A day as its header names it: today, yesterday, or its date.
String _dayName(BuildContext context, DateTime day) {
  final l = AppLocalizations.of(context);
  final now = DateTime.now();
  final today = DateTime(now.year, now.month, now.day);
  final locale = Localizations.localeOf(context).toLanguageTag();
  return day == today
      ? l.today
      : day == today.subtract(const Duration(days: 1))
      ? l.yesterday
      : (day.year == today.year ? DateFormat.MMMMd(locale) : DateFormat.yMMMMd(locale)).format(day);
}

/// What a pinch paints, fixed when two fingers come down: each number of columns' scroll
/// offset (the tile under the fingers at the same place in all), the layout's groups, the
/// thumbnails as decoded images, the days' names, the photos picked.
class _Pinch {
  _Pinch({
    required this.columns,
    required this.offsets,
    required this.groups,
    required this.headers,
    required this.box,
    required this.focal,
    required this.background,
    required this.chip,
    required this.strong,
    required this.days,
  });

  final int columns; // the grid shown when it began
  late int built = columns; // the real grid's, behind the painting
  final Map<int, double> offsets;
  final List<int> groups;
  final bool headers;
  final Size box;
  final Offset focal;
  final Color background, chip, strong;
  final List<TextPainter> days;
  final images = <int, ui.Image>{};
  final picked = <int>{};
  final streams = <(ImageStream, ImageStreamListener)>[];

  /// The images let go: the cache keeps them for the grid.
  void release() {
    for (final (stream, listener) in streams) {
      stream.removeListener(listener);
    }
    streams.clear();
    for (final d in days) {
      d.dispose();
    }
  }
}

/// Paints a pinch at each frame (a phone's gallery, its video of 18 November): the grid shown,
/// zoomed by the fingers around the point between them, fading out as the next one, at the
/// scale that keeps the same photo there, fades in. Thumbnails drawn as images, cropped to
/// squares; nothing is built.
class _PinchPainter extends CustomPainter {
  _PinchPainter(this.pinch, this.grid) : super(repaint: Listenable.merge([grid._scaleN, grid._painted]));

  final _Pinch pinch;
  final _MediaGridState grid;

  static const _gap = 2.0, _header = 52.0;

  @override
  void paint(Canvas canvas, Size size) {
    canvas.drawRect(Offset.zero & size, Paint()..color = pinch.background);
    final c = grid._shownColumns, t = grid._target, s = grid._scale;
    final fade = grid._progress;
    _level(canvas, size, c, s, 1 - fade, labels: fade < 0.5);
    if (t != null && fade > 0) _level(canvas, size, t, s / (c / t), fade, labels: fade >= 0.5);
  }

  void _level(Canvas canvas, Size size, int n, double s, double alpha, {required bool labels}) {
    if (alpha <= 0.002) return;
    final off = pinch.offsets[n];
    if (off == null) return;
    final f = pinch.focal;
    final stride = (pinch.box.width - (n - 1) * _gap) / n + _gap;
    final tile = stride - _gap;
    // The part of the grid on screen at this scale.
    final top = off + f.dy - f.dy / s, bottom = off + f.dy + (size.height - f.dy) / s;
    Rect zoom(Rect r) => Rect.fromLTRB(f.dx + (r.left - f.dx) * s, f.dy + (r.top - f.dy) * s, f.dx + (r.right - f.dx) * s, f.dy + (r.bottom - f.dy) * s);
    final image = Paint()
      ..color = Color.fromRGBO(255, 255, 255, alpha)
      ..filterQuality = FilterQuality.low;
    final chip = Paint()..color = pinch.chip.withValues(alpha: pinch.chip.a * alpha);
    final edge = Paint()
      ..color = pinch.strong.withValues(alpha: alpha)
      ..style = PaintingStyle.stroke
      ..strokeWidth = 3 * s;
    var y = 0.0;
    var first = 0;
    for (var g = 0; g < pinch.groups.length; g++) {
      final count = pinch.groups[g];
      if (pinch.headers) {
        if (labels && y + _header >= top && y <= bottom && g < pinch.days.length) {
          canvas.save();
          canvas.translate(f.dx, f.dy);
          canvas.scale(s);
          canvas.translate(-f.dx, -f.dy);
          pinch.days[g].paint(canvas, Offset(16, y - off + 14));
          canvas.restore();
        }
        y += _header;
      }
      final rows = (count + n - 1) ~/ n;
      final height = rows * stride - _gap;
      if (y + height >= top && y <= bottom) {
        final rowFrom = math.max(0, ((top - y) / stride).floor());
        final rowTo = math.min(rows - 1, ((bottom - y) / stride).floor());
        for (var row = rowFrom; row <= rowTo; row++) {
          for (var col = 0; col < n; col++) {
            final k = row * n + col;
            if (k >= count) break;
            final i = first + k;
            final dst = zoom(Rect.fromLTWH(col * stride, y + row * stride - off, tile, tile));
            final img = pinch.images[i];
            if (img == null) {
              canvas.drawRect(dst, chip);
            } else {
              // Cropped to a square, as BoxFit.cover does.
              final w = img.width.toDouble(), h = img.height.toDouble();
              final side = math.min(w, h);
              final src = Rect.fromLTWH((w - side) / 2, (h - side) / 2, side, side);
              canvas.drawImageRect(img, src, dst, image);
            }
            if (pinch.picked.contains(i)) canvas.drawRect(dst.deflate(1.5 * s), edge);
          }
        }
      }
      y += height;
      first += count;
    }
  }

  @override
  bool shouldRepaint(_PinchPainter old) => old.pinch != pinch;
}

/// A day's name, and « Tout ce jour » with its square box.
class _DayHeader extends StatelessWidget {
  const _DayHeader({required this.day, required this.items});

  final DateTime day;
  final List<MediaEntry> items;

  // It listens to the selection itself: the grid around it is kept as it is while its photos do
  // not change, and a pick no longer showed (real test of 22 November 2026).
  @override
  Widget build(BuildContext context) => ListenableBuilder(listenable: AppScope.of(context).state.selection, builder: (context, _) => _build(context));

  Widget _build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final name = _dayName(context, day);
    final picks = [for (final m in items) Explorer.pickMedia(m)];
    final all = state.selection.allOf(picks);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 6, 6, 2),
      child: Row(
        children: [
          Expanded(child: _Decor(dim: 0.35, child: Text(name, style: titleStyle(14)))),
          _Decor(
            child: BoxToggle(
              label: l.selectDay,
              semanticLabel: l.selectDayLabel(name),
              on: all,
              small: true,
              onPressed: () => state.selection.toggleAll(picks),
            ),
          ),
        ],
      ),
    );
  }
}

/// A photo: all of it picks; the square box and a thick edge say it is picked.
class _MediaTile extends StatelessWidget {
  const _MediaTile({required this.kind, required this.entry, this.columns = 3});

  final MediaKind kind;
  final MediaEntry entry;
  final int columns;

  // It listens to the selection itself: the grid around it is kept as it is while its photos do
  // not change, and a pick no longer showed (real test of 22 November 2026).
  @override
  Widget build(BuildContext context) => ListenableBuilder(listenable: AppScope.of(context).state.selection, builder: (context, _) => _build(context));

  Widget _build(BuildContext context) {
    final state = AppScope.of(context).state;
    final c = context.colors;
    final m = entry;
    final pick = Explorer.pickMedia(m);
    final on = state.selection.contains(pick.path);
    return Semantics(
      button: true,
      toggled: on,
      label: m.name,
      child: GestureDetector(
        key: ValueKey('media-${m.id}'),
        onTap: () => state.selection.toggle(pick),
        child: Stack(
          fit: StackFit.expand,
          children: [
            LibraryImage(cacheKey: '${kind.name}:${m.id}', decodePx: gridDecodePx(context), load: (px) => state.library.thumbnail(kind, m.id, px)),
            if (on) DecoratedBox(decoration: BoxDecoration(border: Border.all(color: c.strong, width: columns >= 5 ? 2 : 3))),
            if (kind == MediaKind.video)
              Positioned(
                left: 4,
                bottom: 4,
                child: _Decor(
                  child: Container(
                  padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
                  decoration: BoxDecoration(color: Colors.black.withValues(alpha: 0.72), borderRadius: BorderRadius.circular(6)),
                  child: Text(
                    formatDuration(context, m.durationMs ~/ 1000),
                    style: TextStyle(color: Colors.white, fontSize: columns >= 4 ? 10 : 11, fontWeight: FontWeight.w700, fontFeatures: tabular),
                    ),
                  ),
                ),
              ),
            Positioned(right: 5, top: 5, child: _Decor(child: PickBox(on: on, onImage: true, size: columns >= 5 ? 18 : 22))),
          ],
        ),
      ),
    );
  }
}

/// A photo or a video as a row of the list layout: its thumbnail, name, details, box.
class _MediaRow extends StatelessWidget {
  const _MediaRow({required this.kind, required this.entry});

  final MediaKind kind;
  final MediaEntry entry;

  // It listens to the selection itself: the grid around it is kept as it is while its photos do
  // not change, and a pick no longer showed (real test of 22 November 2026).
  @override
  Widget build(BuildContext context) => ListenableBuilder(listenable: AppScope.of(context).state.selection, builder: (context, _) => _build(context));

  Widget _build(BuildContext context) {
    final state = AppScope.of(context).state;
    final c = context.colors;
    final m = entry;
    final pick = Explorer.pickMedia(m);
    final on = state.selection.contains(pick.path);
    final when = DateTime.fromMillisecondsSinceEpoch(m.modified * 1000);
    final date = MaterialLocalizations.of(context).formatShortDate(when);
    return Padding(
      padding: const EdgeInsets.only(bottom: 2),
      child: Material(
        color: on ? c.soft : Colors.transparent,
        borderRadius: BorderRadius.circular(14),
        child: InkWell(
          key: ValueKey('media-${m.id}'),
          borderRadius: BorderRadius.circular(14),
          onTap: () => state.selection.toggle(pick),
          child: Semantics(
            toggled: on,
            child: Padding(
              padding: const EdgeInsets.all(6),
              child: Row(
                children: [
                  ClipRRect(
                    borderRadius: BorderRadius.circular(10),
                    child: SizedBox.square(
                      dimension: 48,
                      child: LibraryImage(cacheKey: '${kind.name}:${m.id}', load: (px) => state.library.thumbnail(kind, m.id, px)),
                    ),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(m.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w700)),
                        Text(
                          [date, if (kind == MediaKind.video) formatDuration(context, m.durationMs ~/ 1000), formatBytes(context, m.size)].join(' · '),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(fontSize: 12, color: c.muted, fontFeatures: tabular),
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(width: 8),
                  PickBox(on: on, size: 22),
                  const SizedBox(width: 4),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// Music, newest first, in a list.
class MusicTab extends StatelessWidget {
  const MusicTab({super.key});

  @override
  Widget build(BuildContext context) => const _MediaGate(child: _MusicList());
}

class _MusicList extends StatefulWidget {
  const _MusicList();

  @override
  State<_MusicList> createState() => _MusicListState();
}

class _MusicListState extends State<_MusicList> with AutomaticKeepAliveClientMixin {
  @override
  bool get wantKeepAlive => true;

  @override
  void initState() {
    super.initState();
    final explorer = AppScope.of(context, listen: false).state.explorer;
    if (explorer.media(MediaKind.audio).items.isEmpty) unawaited(explorer.more(MediaKind.audio));
  }

  @override
  Widget build(BuildContext context) {
    super.build(context);
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return ListenableBuilder(
      listenable: Listenable.merge([state.explorer, state.selection]),
      builder: (context, _) {
        final list = state.explorer.media(MediaKind.audio);
        if (list.items.isEmpty) {
          return list.loading || !list.done ? const Center(child: CircularProgressIndicator()) : _Message(text: list.error ?? l.nothingHere);
        }
        return NotificationListener<ScrollNotification>(
          onNotification: (n) {
            if (n.metrics.extentAfter < 1500) unawaited(state.explorer.more(MediaKind.audio));
            return false;
          },
          child: ListView.builder(
            key: const Key('music-list'),
            padding: EdgeInsets.fromLTRB(8, 0, 8, bottomRoom(context)),
            itemCount: list.items.length,
            itemExtent: 60,
            itemBuilder: (context, i) {
              final m = list.items[i];
              final pick = Explorer.pickMedia(m);
              final on = state.selection.contains(pick.path);
              return Padding(
                padding: const EdgeInsets.only(bottom: 2),
                child: Material(
                  color: on ? c.soft : Colors.transparent,
                  borderRadius: BorderRadius.circular(16),
                  child: InkWell(
                    key: ValueKey('music-${m.id}'),
                    borderRadius: BorderRadius.circular(16),
                    onTap: () => state.selection.toggle(pick),
                    child: Semantics(
                      toggled: on,
                      child: Padding(
                        padding: const EdgeInsets.all(8),
                        child: Row(
                          children: [
                            Container(
                              width: 42,
                              height: 42,
                              decoration: BoxDecoration(color: c.section, borderRadius: BorderRadius.circular(10)),
                              child: Icon(Icons.music_note_rounded, color: c.accent),
                            ),
                            const SizedBox(width: 12),
                            Expanded(
                              child: Column(
                                mainAxisAlignment: MainAxisAlignment.center,
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Text(m.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w700)),
                                  Text(
                                    [if (m.album.isNotEmpty) m.album, formatDuration(context, m.durationMs ~/ 1000), formatBytes(context, m.size)].join(' · '),
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    style: TextStyle(fontSize: 12, color: c.muted, fontFeatures: tabular),
                                  ),
                                ],
                              ),
                            ),
                            const SizedBox(width: 8),
                            PickBox(on: on, size: 22),
                            const SizedBox(width: 4),
                          ],
                        ),
                      ),
                    ),
                  ),
                ),
              );
            },
          ),
        );
      },
    );
  }
}

/// The storage's folders, by path (with the access to all files); the system's picker otherwise.
class FilesTab extends StatefulWidget {
  const FilesTab({super.key, required this.onSystemPicker});

  final VoidCallback onSystemPicker;

  @override
  State<FilesTab> createState() => _FilesTabState();
}

/// The kinds of file with a thumbnail of their own (Library.kt's fileThumbnail).
const _thumbed = {
  'jpg',
  'jpeg',
  'png',
  'gif',
  'webp',
  'heic',
  'heif',
  'bmp',
  'avif',
  'mp4',
  'mkv',
  'mov',
  'avi',
  'webm',
  '3gp',
  'm4v',
  'ts',
  'wmv',
  'pdf',
  'apk',
};

String _ext(String name) {
  final dot = name.lastIndexOf('.');
  return dot < 0 ? '' : name.substring(dot + 1).toLowerCase();
}

IconData _iconOf(FileEntry e) {
  if (e.dir) return Icons.folder_rounded;
  return switch (_ext(e.name)) {
    'mp3' || 'm4a' || 'aac' || 'flac' || 'wav' || 'ogg' || 'opus' => Icons.music_note_rounded,
    'zip' || 'rar' || '7z' || 'tar' || 'gz' || 'tgz' || 'bz2' || 'xz' => Icons.folder_zip_rounded,
    'epub' || 'mobi' || 'azw' || 'azw3' || 'fb2' || 'djvu' || 'cbz' || 'cbr' => Icons.menu_book_rounded,
    'doc' || 'docx' || 'odt' || 'rtf' || 'txt' || 'md' => Icons.description_rounded,
    'xls' || 'xlsx' || 'ods' || 'csv' => Icons.table_chart_rounded,
    'ppt' || 'pptx' || 'odp' => Icons.slideshow_rounded,
    'apks' || 'xapk' || 'apkm' => Icons.android_rounded,
    _ => Icons.insert_drive_file_outlined,
  };
}

class _FilesTabState extends State<FilesTab> with AutomaticKeepAliveClientMixin {
  bool? _allFiles;

  @override
  bool get wantKeepAlive => true;

  @override
  void initState() {
    super.initState();
    unawaited(_check());
  }

  Future<void> _check() async {
    final state = AppScope.of(context, listen: false).state;
    final granted = await state.storage.allFiles();
    if (!mounted) return;
    setState(() => _allFiles = granted);
    if (granted && state.explorer.entries == null) await state.explorer.open(state.explorer.dir);
  }

  @override
  Widget build(BuildContext context) {
    super.build(context);
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    if (_allFiles == null) return const Center(child: CircularProgressIndicator());
    if (_allFiles == false) {
      return _Message(
        text: l.filesHint,
        action: Wrap(
          spacing: 8,
          runSpacing: 8,
          alignment: WrapAlignment.center,
          children: [
            FilledButton(
              key: const Key('files-allow'),
              onPressed: () async {
                await state.storage.askAllFiles();
                await _check();
              },
              child: Text(l.storageAllow),
            ),
            OutlinedButton(key: const Key('files-picker'), onPressed: widget.onSystemPicker, child: Text(l.systemPicker)),
          ],
        ),
      );
    }
    final labels = {
      null: l.catFolders,
      FileCategory.documents: l.catDocuments,
      FileCategory.ebooks: l.catEbooks,
      FileCategory.apks: l.catApks,
      FileCategory.archives: l.catArchives,
      FileCategory.large: l.catLarge,
    };
    return ListenableBuilder(
      listenable: Listenable.merge([state.explorer, state.selection]),
      builder: (context, _) {
        final ex = state.explorer;
        final cat = ex.category;
        return Column(
          children: [
            SizedBox(
              height: 56,
              child: ListView(
                scrollDirection: Axis.horizontal,
                padding: const EdgeInsets.fromLTRB(12, 0, 12, 12),
                children: [
                  for (final e in labels.entries)
                    Padding(
                      padding: const EdgeInsets.only(right: 6),
                      child: ChoicePill(
                        key: ValueKey('cat-${e.key?.name ?? 'folders'}'),
                        label: e.value,
                        on: cat == e.key,
                        onPressed: () => ex.showCategory(e.key),
                      ),
                    ),
                ],
              ),
            ),
            Expanded(child: cat == null ? _folders(context, ex) : _category(context, ex, cat)),
          ],
        );
      },
    );
  }

  Widget _folders(BuildContext context, Explorer ex) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final entries = ex.entries;
    final parent = ex.parent;
    final shown = ex.dir.replaceFirst(state.library.storageRoot, '').replaceAll('/', ' › ');
    return Column(
      children: [
        ListTile(
          dense: true,
          leading: IconButton(
            tooltip: l.upFolder,
            icon: const Icon(Icons.arrow_upward_rounded),
            onPressed: parent == null ? null : () => unawaited(ex.open(parent)),
          ),
          title: Text(
            shown.isEmpty ? '/' : shown,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: TextStyle(color: c.muted),
          ),
        ),
        Expanded(
          child: entries == null
              ? const Center(child: CircularProgressIndicator())
              : entries.isEmpty
              ? _Message(text: ex.dirError ?? l.nothingHere)
              : ListView.builder(
                  key: ValueKey('files-list:${ex.dir}'), // a new folder opens at its top
                  padding: EdgeInsets.fromLTRB(8, 0, 8, bottomRoom(context)),
                  itemCount: entries.length,
                  itemExtent: 58,
                  itemBuilder: (context, i) => _FileRow(entry: entries[i], onOpen: () => unawaited(ex.open(entries[i].path))),
                ),
        ),
      ],
    );
  }

  Widget _category(BuildContext context, Explorer ex, FileCategory cat) {
    final l = AppLocalizations.of(context);
    final list = ex.files(cat);
    if (list.items.isEmpty) {
      return list.loading || !list.done ? const Center(child: CircularProgressIndicator()) : _Message(text: list.error ?? l.nothingHere);
    }
    return NotificationListener<ScrollNotification>(
      onNotification: (n) {
        if (n.metrics.extentAfter < 1500) unawaited(ex.moreFiles(cat));
        return false;
      },
      child: ListView.builder(
        key: ValueKey('files-${cat.name}'),
        padding: EdgeInsets.fromLTRB(8, 0, 8, bottomRoom(context)),
        itemCount: list.items.length,
        itemExtent: 58,
        itemBuilder: (context, i) => _FileRow(entry: list.items[i], showFolder: true),
      ),
    );
  }
}

/// One file or folder: its thumbnail or icon, name, size; a box to pick it.
class _FileRow extends StatelessWidget {
  const _FileRow({required this.entry, this.onOpen, this.showFolder = false});

  final FileEntry entry;
  final VoidCallback? onOpen;

  /// In a category, where the file is.
  final bool showFolder;

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final e = entry;
    final pick = Explorer.pickFile(e);
    final picked = state.selection.items.where((p) => p.path == e.path).firstOrNull;
    void toggle() {
      state.selection.toggle(pick);
      state.explorer.sizeFolders(state.selection);
    }

    final String? sub;
    if (e.dir) {
      sub = picked == null ? null : (picked.size == 0 ? l.sizing : formatBytes(context, picked.size));
    } else {
      final where = showFolder ? e.path.substring(0, e.path.lastIndexOf('/')).replaceFirst(state.library.storageRoot, '') : '';
      sub = [formatBytes(context, e.size), if (where.isNotEmpty) where].join(' · ');
    }
    final thumbed = !e.dir && _thumbed.contains(_ext(e.name));
    final icon = Icon(_iconOf(e), size: 32, color: e.dir ? c.accent : c.muted);
    final on = picked != null;
    // A folder opens; a file is picked. The box, apart, picks a folder whole (no ambiguity).
    final open = e.dir && onOpen != null;
    return Padding(
      padding: const EdgeInsets.only(bottom: 2),
      child: Material(
        color: on ? c.soft : Colors.transparent,
        borderRadius: BorderRadius.circular(16),
        child: Row(
          children: [
            Expanded(
              child: InkWell(
                key: ValueKey('file-${e.name}'),
                borderRadius: BorderRadius.circular(16),
                onTap: open ? onOpen : toggle,
                child: Padding(
                  padding: const EdgeInsets.fromLTRB(8, 0, 4, 0),
                  child: Row(
                    children: [
                      SizedBox.square(
                        dimension: 44,
                        child: thumbed
                            ? ClipRRect(
                                borderRadius: BorderRadius.circular(8),
                                child: LibraryImage(cacheKey: 'file:${e.path}', load: (px) => state.library.fileThumbnail(e.path, px), placeholder: Center(child: icon)),
                              )
                            : icon,
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        child: Column(
                          mainAxisAlignment: MainAxisAlignment.center,
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(e.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w700)),
                            if (sub != null)
                              Text(sub, maxLines: 1, overflow: TextOverflow.ellipsis, style: TextStyle(color: c.muted, fontSize: 12, fontFeatures: tabular)),
                          ],
                        ),
                      ),
                      if (open) Icon(Icons.chevron_right_rounded, color: c.muted),
                    ],
                  ),
                ),
              ),
            ),
            Semantics(
              button: true,
              toggled: on,
              label: e.name,
              excludeSemantics: true,
              child: InkWell(
                borderRadius: BorderRadius.circular(16),
                onTap: toggle,
                child: SizedBox.square(dimension: 48, child: Center(child: PickBox(on: on, size: 22))),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// The room the floating bar of the home screen takes at the bottom of a list.
double bottomRoom(BuildContext context) => MediaQuery.paddingOf(context).bottom + 16;
