/// The tabs of the explorer (WP-B.9, §1.2 of the J6b plan): Apps and Photos in grids, Videos,
/// Music, Files. A tap picks or unpicks; the basket of the home screen shows the selection.
/// Lists are lazy and the images decoded at the size shown (UI-1).
library;

import 'dart:async';
import 'dart:math' as math;
import 'dart:ui' as ui;

import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter/semantics.dart';
import 'package:intl/intl.dart';

import '../../app.dart';
import '../../l10n/app_localizations.dart';
import '../../library/explorer.dart';
import '../viewer/open_media.dart';
import 'grid_layout.dart';
import '../../platform/library.dart';
import '../../theme.dart';
import '../../widgets/format.dart';
import '../../widgets/v4.dart';

/// A thumbnail or an icon from the explorer's cache, decoded at its displayed size.
class LibraryImage extends StatefulWidget {
  const LibraryImage({super.key, required this.cacheKey, required this.load, this.fit = BoxFit.cover, this.placeholder, this.decodePx, this.sharp, this.kept});

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

  /// A sharper image at the size shown, asked once the tile is settled on screen, shown in place
  /// of the first once it comes: the platform's thumbnails stop short of a large tile (about
  /// 270 px on Android, measured on 23 November 2026).
  /// It comes at one size ([sharpSize]) for every layout, decoded as it is: made once on the
  /// platform, decoded once here, the same after a pinch. [kept]: the same, only if it was made
  /// already (read in about 1 ms), else null.
  final Future<Uint8List?> Function()? sharp, kept;

  /// From this size shown, in pixels, the platform's thumbnail is too small.
  static const sharpFrom = 300;

  /// How long a tile stays on screen, still, before its sharp image is asked.
  static const sharpAfter = Duration(milliseconds: 60);

  @override
  State<LibraryImage> createState() => _LibraryImageState();
}

class _LibraryImageState extends State<LibraryImage> {
  Uint8List? _bytes;
  String? _asked;
  late Explorer _explorer;
  ImageProvider? _sharp; // decoded: shown in place of the first image
  String? _sharpKey; // asked, or about to be
  Timer? _sharpTimer;
  bool _sharpPending = false;
  bool _keptAsked = false; // the sharp image made already asked, no answer yet: the first waits
  ImageStream? _sharpStream;
  ImageStreamListener? _sharpListener;
  ImageProvider? _first; // the first image as decoded for [_firstBytes] at [_firstPx]
  Uint8List? _firstBytes;
  int _firstPx = 0;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _explorer = AppScope.of(context, listen: false).state.explorer;
  }

  /// [bytes] decoded at [px], the decoding started at once. Left to [Image], it waited until a
  /// fling slowed down (Flutter puts off decoding while a list moves fast): the tiles flung past
  /// stayed empty, up to 60 % of the grid, and filled 5 to 10 frames after (filmed at 60 frames
  /// a second, 23 November 2026). A thumbnail decodes in a few ms, off the main thread.
  ImageProvider _decoded(Uint8List bytes, int px) {
    final known = _first;
    if (known != null && identical(bytes, _firstBytes) && px == _firstPx) return known;
    final first = ResizeImage(MemoryImage(bytes), width: px); // Image.memory's own key
    _first = first;
    _firstBytes = bytes;
    _firstPx = px;
    unawaited(precacheImage(first, context, onError: (_, _) {}));
    return first;
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

  /// The sharp image of a tile [shown] px wide, from the cache, else asked once the tile has
  /// stayed still on screen for [LibraryImage.sharpAfter]; shown once decoded, so that the first
  /// stays until then, without a frame of placeholder.
  void _fetchSharp(int shown) {
    if (widget.sharp == null || shown < LibraryImage.sharpFrom) return;
    final key = '${widget.cacheKey}@sharp';
    if (_sharpKey == key) return;
    _sharpKey = key;
    _sharpTimer?.cancel();
    final cached = _explorer.cached(key);
    if (cached != null) {
      _decodeSharp(key, cached);
      return;
    }
    // Made already (ahead, or seen before): straight in, without the blurred one first (real
    // test of 23 November 2026: every tile showed Android's thumbnail, then the sharp one). Not
    // while flinging: hundreds of large images would be read and decoded for nothing.
    final kept = widget.kept;
    if (kept != null && !Scrollable.recommendDeferredLoadingForContext(context)) {
      _keptAsked = true;
      _sharpPending = true;
      _explorer.image(key, kept).then((bytes) {
        if (_sharpKey != key) return;
        _sharpPending = false;
        _keptAsked = false;
        if (!mounted) return;
        if (bytes != null) {
          _decodeSharp(key, bytes);
        } else {
          setState(() {}); // not made yet: the first one, and the sharp one once still
          _askSharpLater(key);
        }
      });
      return;
    }
    _askSharpLater(key);
  }

  /// The sharp image made, once the tile has stayed still on screen for [LibraryImage.sharpAfter].
  void _askSharpLater(String key) {
    final sharp = widget.sharp;
    if (sharp == null) return;
    void ask() {
      if (!mounted || _sharpKey != key) return;
      // Still flinging: later.
      if (Scrollable.recommendDeferredLoadingForContext(context)) {
        _sharpTimer = Timer(LibraryImage.sharpAfter, ask);
        return;
      }
      _sharpPending = true;
      _explorer.image(key, sharp, low: true).then((bytes) {
        if (_sharpKey == key) _sharpPending = false;
        if (mounted && _sharpKey == key && bytes != null) {
          _decodeSharp(key, bytes);
        }
      });
    }

    _sharpTimer = Timer(LibraryImage.sharpAfter, ask);
  }

  /// Decodes [bytes]; at once when already decoded (after a pinch), else in its time.
  void _decodeSharp(String key, Uint8List bytes) {
    final provider = MemoryImage(bytes);
    _dropSharpStream();
    final stream = provider.resolve(createLocalImageConfiguration(context));
    var building = true;
    final listener = ImageStreamListener(
      (_, _) {
        if (!mounted || _sharpKey != key || _sharp == provider) return;
        if (building) {
          _sharp = provider; // decoded already: this very build shows it
        } else {
          setState(() => _sharp = provider);
        }
      },
      // Not decodable (a file damaged): the first one stays, rather than an empty tile.
      onError: (_, _) {
        if (_sharpKey != key) return;
        _dropSharpStream();
        if (mounted && !building) setState(() {});
      },
    );
    stream.addListener(listener);
    building = false;
    _sharpStream = stream;
    _sharpListener = listener;
  }

  void _dropSharpStream() {
    final l = _sharpListener;
    if (l != null) _sharpStream?.removeListener(l);
    _sharpStream = null;
    _sharpListener = null;
  }

  @override
  void dispose() {
    // Scrolled away before its turn: not loaded at all.
    if (_bytes == null && _asked != null) _explorer.forget(_asked!);
    _sharpTimer?.cancel();
    _dropSharpStream();
    if (_sharpPending && _sharpKey != null) _explorer.forget(_sharpKey!);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, box) {
        final dpr = MediaQuery.devicePixelRatioOf(context);
        final shown = (box.biggest.shortestSide * dpr).round().clamp(32, 1024);
        final px = widget.decodePx ?? shown;
        _fetchSharp(shown);
        final sharp = _sharp;
        // Shrunk with the finest filter: a few per cent less with a plain one made it soft.
        if (sharp != null) return Image(image: sharp, fit: widget.fit, gaplessPlayback: true, filterQuality: FilterQuality.high, excludeFromSemantics: true);
        if (_keptAsked || (_sharpStream != null && _bytes == null)) {
          // The sharp one is coming (read, or decoding): the first only if it is at hand already.
          final first = _explorer.cached(widget.cacheKey);
          if (first == null) return widget.placeholder ?? ColoredBox(color: context.colors.chip);
          return Image(image: _decoded(first, px), fit: widget.fit, gaplessPlayback: true, filterQuality: FilterQuality.medium, excludeFromSemantics: true);
        }
        _fetch(px);
        final bytes = _bytes;
        if (bytes == null) return widget.placeholder ?? ColoredBox(color: context.colors.chip);
        return Image(image: _decoded(bytes, px), fit: widget.fit, gaplessPlayback: true, filterQuality: FilterQuality.medium, excludeFromSemantics: true);
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
/// The line between the grid's photos: a hint of the theme's accent in the background, so that
/// the grid takes the colours chosen (asked on 23 November 2026, seen in a phone's gallery).
Color seamOf(MedxferColors c) => Color.alphaBlend(c.accent.withValues(alpha: 0.5), c.background);

/// The one size of the sharp images, in pixels: as tall as the tallest mosaic row, at most as
/// wide as the screen; each in its own proportions, 3 columns showing a square of it (23
/// November 2026: one size made, kept and decoded for every layout). Always shown smaller, never
/// stretched: at the aimed height (702 px on the A57) the tallest rows stretched it, and the
/// others shrank it by a few per cent, both soft (real test of 23 November 2026).
({int height, int width}) sharpSize(BuildContext context) {
  // The short side: turned sideways, the screen keeps its size (23 November 2026: made again at
  // 1167 px, then again at 537 once turned back).
  final width = MediaQuery.sizeOf(context).shortestSide * MediaQuery.devicePixelRatioOf(context);
  return (height: (width * GridLayout.maxRowShare).round().clamp(32, 1728), width: width.round().clamp(32, 2160));
}

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
  final ScrollController _scroll = ScrollController();

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

  // Where each photo lies, layout by layout (2: the mosaic), for the photos and width shown.
  final _layouts = <int, GridLayout>{};
  Object? _layoutsKey;
  List<double> _aspects = const [];

  GridLayout _layoutOf(int columns) => _layouts[columns] ??= columns <= Explorer.minColumns
      ? GridLayout.mosaic(groups: _groups, headers: _headers, width: _box.width, aspects: _aspects, gap: _gap, header: _headerHeight)
      : GridLayout.squares(groups: _groups, headers: _headers, width: _box.width, columns: columns, gap: _gap, header: _headerHeight);

  // The photos as the grid shows them (an album chosen, or all).
  List<MediaEntry> _shownItems = const [];

  /// [m] brought into view, if out of it: the viewer of photos closes on it, and it flies back
  /// into its tile (swiped far from where it opened, it flew to nothing, 23 November 2026).
  void _reveal(MediaEntry m) {
    if (!mounted || !_scroll.hasClients || _box.isEmpty) return;
    var i = _shownItems.indexWhere((x) => identical(x, m));
    if (i < 0) i = _shownItems.indexWhere((x) => x.id == m.id);
    if (i < 0) return;
    final top = _top(i, _shownColumns, _shownList);
    final h = _shownList ? (_rows.extent(i) ?? _rowHeight) : _layoutOf(_shownColumns).rectOf(i).height;
    final p = _scroll.position;
    final view = p.viewportDimension;
    if (top >= p.pixels && top + h <= p.pixels + view - 96) return; // in view, above the floating bar
    p.jumpTo((top - (view - h) / 2).clamp(p.minScrollExtent, p.maxScrollExtent).toDouble());
  }

  // The photo under the fingers, its height in each layout: how much a step zooms. Square tiles
  // go by the ratio of their columns; the mosaic's rows each have their height.
  Map<int, double> _anchorHeights = const {};

  /// How much larger the grid's photos are in layout [to] than in [from]: never so near 1 that
  /// a step would not show (a panorama in the mosaic is about as tall as a tile of 3 columns).
  double _ratio(int from, int to) {
    final a = _anchorHeights[from], b = _anchorHeights[to];
    final r = a != null && b != null && a > 0 && b > 0 ? b / a : from / to;
    return to < from ? math.max(r, 1.2) : math.min(r, 1 / 1.2);
  }
  int _shownColumns = 0;
  bool _shownList = false;

  // The same photo, the same tile: at a pinch's end, those still on screen are kept whole, not
  // built again; only what sizes by the columns is ([_TileColumns]). The frame of a pinch's end
  // built ~170 tiles in 25 to 72 ms (measured on the A57, 23 November 2026).
  final _tiles = Expando<Widget>('tiles');

  static const _headerHeight = 52.0;
  static const _rowHeight = 70.0; // a row with its 56 px thumbnail (real test of 22 November 2026: larger icons)
  final _rows = _RowHeights(); // the list layout's rows, a long name in full
  // The line between the photos: about 2 pixels, as a phone's gallery draws it (measured on
  // the A57, 23 November 2026: 2 px there, 5 to 6 here at 2 dp), in a hint of the theme ([seamOf]).
  static const _gap = 0.75;

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
    final k = _ratio(_shownColumns, t);
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
    _anchorHeights = {for (var n = Explorer.minColumns; n <= Explorer.maxColumns; n++) n: _layoutOf(n).rectOf(index).height};
    final offsets = <int, double>{c: _scroll.offset};
    for (var t = Explorer.minColumns; t <= Explorer.maxColumns; t++) {
      if (t != c) offsets[t] = (_top(index, t, false) + within * _ratio(c, t) - _focal.dy).clamp(0.0, double.infinity).toDouble();
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
      layouts: {for (var n = Explorer.minColumns; n <= Explorer.maxColumns; n++) n: _layoutOf(n)},
      wanted: wanted,
      box: _box,
      focal: _focal,
      background: colors.background,
      seam: seamOf(colors),
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
      // The same image as the grid's tile, decoded already: the sharp one as it is, else the
      // first at cacheWidth px (LibraryImage).
      final sharp = state.explorer.cached('${widget.kind.name}:${m.id}@sharp');
      final bytes = sharp ?? state.explorer.cached('${widget.kind.name}:${m.id}');
      if (bytes == null) continue;
      final ImageProvider provider = sharp != null ? MemoryImage(sharp) : ResizeImage(MemoryImage(bytes), width: px);
      final stream = provider.resolve(config);
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
  /// which goes a frame after.
  ///
  /// The same grid, its tiles kept: those of the photos still on screen are updated, only the
  /// others made. Built anew, the frame took 78 to 138 ms, 36 to 115 tiles at ~1.5 ms each; the
  /// other screens, told the next frame, 16 to 63 ms more, for nothing: the layout's menu, the
  /// only other to read it, is not open during a pinch (measured on the A57, 23 November 2026).
  void _endPinch(Explorer ex) {
    final pinch = _pinchN.value;
    if (pinch == null) return;
    final ending = ++_ending;
    final c = _shownColumns;
    final changed = c != pinch.built;
    final target = pinch.offsets[c] ?? 0;
    if (changed) {
      pinch.built = c;
      ex.setLayout(widget.kind, columns: c, asList: false, quiet: true);
      if (_scroll.hasClients) {
        // What the scroll does on its own first ends: the fingers lifted while sliding, it went
        // on toward the end of the list (real test of 23 November 2026), under the painting
        // then in sight. Then the offset of the new layout, without a scroll to it: the layout
        // of this very frame, under the painting, places the grid there.
        final position = _scroll.position;
        position.jumpTo(position.pixels);
        position.correctPixels(target);
      }
      setState(() {});
    } else if (_scroll.hasClients) {
      _scroll.jumpTo(target); // two fingers may have scrolled it, under the painting
    }
    // Two frames, each asked for: nothing else may move on screen to bring them.
    final binding = WidgetsBinding.instance;
    binding.addPostFrameCallback((_) {
      if (!mounted) return;
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
    if (asList) {
      final tops = _rows.tops;
      return index < tops.length ? tops[index] : index * _rowHeight;
    }
    final layout = _layoutOf(columns);
    return index < layout.count ? layout.rectOf(index).top : layout.extent;
  }

  /// The item at height [y] of a layout, and at [x] across (the column under the fingers).
  int _at(double y, int columns, bool asList, {double x = 0}) {
    final count = _groups.fold(0, (a, b) => a + b);
    if (count == 0) return 0;
    if (asList) {
      final tops = _rows.tops;
      if (tops.length < 2) return (y / _rowHeight).floor().clamp(0, count - 1);
      var lo = 0, hi = tops.length - 2; // the last row whose top is at or above y
      while (lo < hi) {
        final mid = (lo + hi + 1) ~/ 2;
        if (tops[mid] <= y) {
          lo = mid;
        } else {
          hi = mid - 1;
        }
      }
      return lo.clamp(0, count - 1);
    }
    return _layoutOf(columns).at(y, x: x).clamp(0, count - 1);
  }

  /// A new layout: the item at [anchor] (from the top of the view) stays at that height.
  void _keep(int fromColumns, bool fromList, int toColumns, bool toList, double anchor) {
    if (!_scroll.hasClients || _box.width <= 0) return;
    final index = _at(_scroll.offset + anchor, fromColumns, fromList);
    final within = _scroll.offset + anchor - _top(index, fromColumns, fromList);
    final fromHeight = fromList ? 0.0 : _layoutOf(fromColumns).rectOf(index).height;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_scroll.hasClients) return;
      // The same share of the photo above the anchor, at its new height.
      final toHeight = toList || index >= _layoutOf(toColumns).count ? 0.0 : _layoutOf(toColumns).rectOf(index).height;
      final ratio = fromHeight > 0 ? toHeight / fromHeight : 0.0;
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
    _scale = math.pow(_ratio(c, t), pos.abs()).toDouble(); // the painting follows; nothing is built
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
    _settleGoal = t != null && _progress >= 0.5 ? _ratio(_shownColumns, t) : 1;
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
        // Changed from the menu: the tile at the top of the view stays at the top. A pinch keeps
        // its own anchor, under the fingers, and its own level: a build while it is painted (a
        // page loaded, a pick) leaves both (23 November 2026: it moved the list, the grid kept).
        final pinching = _pinchN.value != null;
        if (!pinching && _shownColumns != 0 && (_shownColumns != columns || _shownList != asList) && _startSpread == null) {
          _keep(_shownColumns, _shownList, columns, asList, 0);
        }
        _shownItems = items;
        _groups = asList ? [items.length] : [for (final d in days) d.$2.length];
        _headers = byDay && !asList;
        if (!pinching) {
          _shownColumns = columns;
          _shownList = asList;
        }
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
                    if (asList) _rows.measure(context, items, box.maxWidth - 16);
                    final layoutsKey = (list.items, items.length, _album, byDay, ex.order(widget.kind), box.maxWidth);
                    if (layoutsKey != _layoutsKey) {
                      _layoutsKey = layoutsKey;
                      _layouts.clear();
                      _aspects = [for (final m in items) m.aspect];
                    }
                    Widget view(ScrollController controller, int cols, Key key) => _TileColumns(
                      columns: cols,
                      child: CustomScrollView(
                      key: key,
                      controller: controller,
                      physics: const _PinchablePhysics(),
                      slivers: [
                        if (asList)
                          SliverPadding(
                            padding: const EdgeInsets.symmetric(horizontal: 8),
                            sliver: SliverVariedExtentList.builder(
                              itemExtentBuilder: (i, _) => _rows.extent(i) ?? _rowHeight,
                              itemCount: items.length,
                              itemBuilder: (context, i) => _MediaRow(kind: widget.kind, entry: items[i]),
                            ),
                          )
                        else
                          for (final (g, day) in days.indexed) ...[
                            if (byDay) SliverToBoxAdapter(child: SizedBox(height: _headerHeight, child: _DayHeader(day: day.$1, items: day.$2))),
                            if (cols <= Explorer.minColumns) _mosaic(items, _layoutOf(cols), g) else _grid(day.$2, cols),
                          ],
                        SliverToBoxAdapter(child: SizedBox(height: bottomRoom(context))),
                      ],
                    ),
                    );
                    // The same widget while nothing it shows changed: not rebuilt.
                    final shows = (list.items, items.length, _album, asList, byDay, ex.order(widget.kind), box.biggest, columns, MediaQuery.textScalerOf(context));
                    final hit = _view;
                    final grid = hit != null && hit.$1 == shows ? hit.$2 : view(_scroll, columns, ValueKey('media-${widget.kind.name}'));
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
    delegate: SliverChildBuilderDelegate((context, i) => _tiles[items[i]] ??= _MediaTile(kind: widget.kind, entry: items[i]), childCount: items.length),
  );

  /// A day of the mosaic ([GridLayout.mosaic]): its rows, each its photos at their places.
  Widget _mosaic(List<MediaEntry> items, GridLayout layout, int group) {
    final rows = layout.groupRows[group];
    return SliverVariedExtentList.builder(
      itemCount: rows.length,
      itemExtentBuilder: (r, _) => r < rows.length ? rows[r].height + _gap : null,
      itemBuilder: (context, r) {
        final row = rows[r];
        return Stack(
          children: [
            for (var k = row.first; k < row.first + row.count; k++)
              Positioned(
                left: layout.rectOf(k).left,
                top: 0,
                width: layout.rectOf(k).width,
                height: row.height,
                child: _tiles[items[k]] ??= _MediaTile(kind: widget.kind, entry: items[k]),
              ),
          ],
        );
      },
    );
  }

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
    required this.layouts,
    required this.wanted,
    required this.box,
    required this.focal,
    required this.background,
    required this.chip,
    required this.strong,
    required this.seam,
    required this.days,
  });

  final int columns; // the grid shown when it began
  late int built = columns; // the real grid's, behind the painting
  final Map<int, double> offsets;
  final Map<int, GridLayout> layouts;
  final Set<int> wanted; // the photos the grids may show during the gesture
  final Size box;
  final Offset focal;
  final Color background, chip, strong, seam;
  final List<TextPainter> days;
  final images = <int, ui.Image>{};
  final _morphs = <int, Set<int>>{};

  /// Between the mosaic and a grid, the photos that keep their column ([from] and [to] laid as
  /// the fingers place them): they change shape rather than fade, as a phone's gallery does with
  /// three tall photos to a row (its video of 23 November 2026). Those that change row or column
  /// (a landscape photo alone on its row) fade, with the zoom: moved, they would cross the
  /// others. Between grids of 3 to 5 columns, none: all fade, as agreed on 22 November.
  Set<int> morphs(int from, int to, double height) {
    if (from > Explorer.minColumns && to > Explorer.minColumns) return const {};
    return _morphs[from * 16 + to] ??= () {
      final a = layouts[from], b = layouts[to];
      final oa = offsets[from], ob = offsets[to];
      if (a == null || b == null || oa == null || ob == null) return const <int>{};
      final out = <int>{};
      for (final i in wanted) {
        if (i >= a.count || i >= b.count) continue;
        final ra = a.rectOf(i).shift(Offset(0, -oa)), rb = b.rectOf(i).shift(Offset(0, -ob));
        final across = math.min(ra.right, rb.right) - math.max(ra.left, rb.left);
        // Its column kept (half its width at least), and not sent across the screen.
        if (across >= 0.5 * math.min(ra.width, rb.width) && (ra.center.dy - rb.center.dy).abs() <= height * 0.5) out.add(i);
      }
      return out;
    }();
  }
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
/// scale that keeps the same photo there, fades in. Thumbnails drawn as images, cropped to their
/// tiles (squares, or the mosaic's own); nothing is built.
class _PinchPainter extends CustomPainter {
  _PinchPainter(this.pinch, this.grid) : super(repaint: Listenable.merge([grid._scaleN, grid._painted]));

  final _Pinch pinch;
  final _MediaGridState grid;

  @override
  void paint(Canvas canvas, Size size) {
    canvas.drawRect(Offset.zero & size, Paint()..color = pinch.background);
    final c = grid._shownColumns, t = grid._target, s = grid._scale;
    final fade = grid._progress;
    final morphs = t != null && fade > 0 ? pinch.morphs(c, t, size.height) : const <int>{};
    _level(canvas, size, c, s, 1 - fade, labels: fade < 0.5, skip: morphs);
    if (t != null && fade > 0) {
      final st = s / grid._ratio(c, t);
      _level(canvas, size, t, st, fade, labels: fade >= 0.5, skip: morphs);
      if (morphs.isNotEmpty) _morph(canvas, size, c, t, s, st, fade, morphs);
    }
  }

  /// [r] in the grid, [off] its scroll, zoomed by [s] around the fingers.
  Rect _zoom(Rect r, double off, double s) {
    final f = pinch.focal;
    return Rect.fromLTRB(f.dx + (r.left - f.dx) * s, f.dy + (r.top - off - f.dy) * s, f.dx + (r.right - f.dx) * s, f.dy + (r.bottom - off - f.dy) * s);
  }

  /// One photo in [dst], cropped to it as BoxFit.cover does; its half line around it.
  void _tile(Canvas canvas, int i, Rect dst, double s, Paint image, Paint chip, Paint seam, Paint edge) {
    canvas.drawRect(dst.inflate(_MediaGridState._gap * s / 2), seam);
    final img = pinch.images[i];
    if (img == null) {
      canvas.drawRect(dst, chip);
    } else {
      final w = img.width.toDouble(), h = img.height.toDouble();
      final fit = dst.width / dst.height;
      final src = w / h > fit ? Rect.fromLTWH((w - h * fit) / 2, 0, h * fit, h) : Rect.fromLTWH(0, (h - w / fit) / 2, w, w / fit);
      canvas.drawImageRect(img, src, dst, image);
    }
    if (pinch.picked.contains(i)) canvas.drawRect(dst.deflate(1.5 * s), edge);
  }

  /// The photos that change shape: each once, whole, from its place in [c] (zoomed [sc]) to its
  /// place in [t] (zoomed [st]) as the gesture goes.
  void _morph(Canvas canvas, Size size, int c, int t, double sc, double st, double fade, Set<int> morphs) {
    final a = pinch.layouts[c]!, b = pinch.layouts[t]!;
    final oa = pinch.offsets[c]!, ob = pinch.offsets[t]!;
    final screen = Offset.zero & size;
    final s = sc + (st - sc) * fade;
    final image = Paint()..filterQuality = FilterQuality.low;
    final chip = Paint()..color = pinch.chip;
    final seam = Paint()..color = pinch.seam;
    final edge = Paint()
      ..color = pinch.strong
      ..style = PaintingStyle.stroke
      ..strokeWidth = 3 * s;
    for (final i in morphs) {
      final dst = Rect.lerp(_zoom(a.rectOf(i), oa, sc), _zoom(b.rectOf(i), ob, st), fade)!;
      if (dst.overlaps(screen)) _tile(canvas, i, dst, s, image, chip, seam, edge);
    }
  }

  void _level(Canvas canvas, Size size, int n, double s, double alpha, {required bool labels, Set<int> skip = const {}}) {
    if (alpha <= 0.002) return;
    final off = pinch.offsets[n];
    final layout = pinch.layouts[n];
    if (off == null || layout == null) return;
    final f = pinch.focal;
    // The part of the grid on screen at this scale.
    final top = off + f.dy - f.dy / s, bottom = off + f.dy + (size.height - f.dy) / s;
    final image = Paint()
      ..color = Color.fromRGBO(255, 255, 255, alpha)
      ..filterQuality = FilterQuality.low;
    final chip = Paint()..color = pinch.chip.withValues(alpha: pinch.chip.a * alpha);
    final seam = Paint()..color = pinch.seam.withValues(alpha: alpha);
    final edge = Paint()
      ..color = pinch.strong.withValues(alpha: alpha)
      ..style = PaintingStyle.stroke
      ..strokeWidth = 3 * s;
    if (labels) {
      for (var g = 0; g < layout.headerTops.length && g < pinch.days.length; g++) {
        final y = layout.headerTops[g];
        if (y + 52 < top || y > bottom) continue;
        canvas.save();
        canvas.translate(f.dx, f.dy);
        canvas.scale(s);
        canvas.translate(-f.dx, -f.dy);
        pinch.days[g].paint(canvas, Offset(16, y - off + 14));
        canvas.restore();
      }
    }
    layout.visit(top, bottom, (i, r) {
      if (!skip.contains(i)) _tile(canvas, i, _zoom(r, off, s), s, image, chip, seam, edge);
    });
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

/// The grid's number of columns, for what a tile sizes by it (its box, edge, duration): those
/// only are built again when it changes, not the tiles.
class _TileColumns extends InheritedWidget {
  const _TileColumns({required this.columns, required super.child});

  final int columns;

  static int of(BuildContext context) => context.dependOnInheritedWidgetOfExactType<_TileColumns>()?.columns ?? 3;

  @override
  bool updateShouldNotify(_TileColumns old) => old.columns != columns;
}

/// What a tile sizes by the number of columns.
class _ByColumns extends StatelessWidget {
  const _ByColumns(this.build_);

  final Widget Function(int columns) build_;

  @override
  Widget build(BuildContext context) => build_(_TileColumns.of(context));
}

/// A photo: all of it picks; the square box and a thick edge say it is picked.
class _MediaTile extends StatelessWidget {
  const _MediaTile({required this.kind, required this.entry});

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
    final l = AppLocalizations.of(context);
    // One node for the accessibility: its name, picked or not, the tap, and « Ouvrir » (its
    // badges and image said nothing more, and cost a node each at every pinch's end).
    return Semantics(
      button: true,
      toggled: on,
      label: m.name,
      excludeSemantics: true,
      onTap: () => state.selection.toggle(pick),
      customSemanticsActions: {CustomSemanticsAction(label: l.openThis(m.name)): () => _openFromExplorer(context, m)},
      child: GestureDetector(
        key: ValueKey('media-${m.id}'),
        onTap: () => state.selection.toggle(pick),
        // Half the line between two photos, drawn outside: two neighbours make it whole, and a
        // short row leaves no coloured cell after its last photo.
        child: DecoratedBox(
          decoration: BoxDecoration(
            border: Border.all(color: seamOf(c), width: _MediaGridState._gap / 2, strokeAlign: BorderSide.strokeAlignOutside),
          ),
          child: Stack(
          fit: StackFit.expand,
          children: [
            _HeroOf(
              entry: m,
              child: LibraryImage(
              cacheKey: '${kind.name}:${m.id}',
              decodePx: gridDecodePx(context),
              load: (px) => state.library.thumbnail(kind, m.id, px),
              sharp: () {
                final size = sharpSize(context);
                return state.library.sharpThumbnail(m, height: size.height, width: size.width);
              },
              kept: () {
                final size = sharpSize(context);
                return state.library.sharpThumbnail(m, height: size.height, width: size.width, keptOnly: true);
              },
            ),
            ),
            if (on) _ByColumns((columns) => DecoratedBox(decoration: BoxDecoration(border: Border.all(color: c.strong, width: columns >= 5 ? 2 : 3)))),
            if (kind == MediaKind.video)
              Positioned(
                left: 4,
                bottom: 4,
                child: _Decor(
                  child: Container(
                  padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
                  decoration: BoxDecoration(color: Colors.black.withValues(alpha: 0.72), borderRadius: BorderRadius.circular(6)),
                  child: _ByColumns(
                    (columns) => Text(
                      formatDuration(context, m.durationMs ~/ 1000),
                      style: TextStyle(color: Colors.white, fontSize: columns >= 4 ? 10 : 11, fontWeight: FontWeight.w700, fontFeatures: tabular),
                    ),
                  ),
                  ),
                ),
              ),
            Positioned(right: 5, top: 5, child: _Decor(child: _ByColumns((columns) => PickBox(on: on, onImage: true, size: columns >= 5 ? 18 : 22)))),
            // ⤢ opens it, touching it elsewhere picks it: no doubt which (D9, mockup 19).
            Positioned(
              right: 0,
              bottom: 0,
              child: _Decor(
                child: _OpenButton(key: ValueKey('open-${m.id}'), label: l.openThis(m.name), onImage: true, onOpen: () => _openFromExplorer(context, m)),
              ),
            ),
          ],
        ),
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

  static const nameStyle = TextStyle(fontSize: 14, fontWeight: FontWeight.w700);
  static const detailStyle = TextStyle(fontSize: 12, fontFeatures: tabular);

  /// Across a row, all but the name: the list's padding (16), the row's (12), the thumbnail and
  /// its gap (66), the box, its gap and ⤢ (74).
  static const besideName = 168.0; // with ⤢ at the end (44 px), WP-8.2

  /// A row's height for the height of its name and details.
  static double heightFor(double text) => math.max(56.0, text).ceilToDouble() + 12 + 2;

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
                      dimension: 56,
                      child: LibraryImage(cacheKey: '${kind.name}:${m.id}', load: (px) => state.library.thumbnail(kind, m.id, px)),
                    ),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Column(
                      mainAxisAlignment: MainAxisAlignment.center,
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(m.name, style: nameStyle), // in full, on as many lines as it needs
                        Text(
                          [date, if (kind == MediaKind.video) formatDuration(context, m.durationMs ~/ 1000), formatBytes(context, m.size)].join(' · '),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: detailStyle.copyWith(color: c.muted),
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(width: 8),
                  PickBox(on: on, size: 22),
                  _OpenButton(key: ValueKey('open-${m.id}'), label: AppLocalizations.of(context).openThis(m.name), onOpen: () => _openFromExplorer(context, m)),
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
                            PickBox(on: on, size: 22),
                            const SizedBox(width: 12),
                            Expanded(
                              child: Column(
                                mainAxisAlignment: MainAxisAlignment.center,
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Text(m.name, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w700)),
                                  Text(
                                    [if (m.album.isNotEmpty) m.album, formatDuration(context, m.durationMs ~/ 1000), formatBytes(context, m.size)].join(' · '),
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    style: TextStyle(fontSize: 12, color: c.muted, fontFeatures: tabular),
                                  ),
                                ],
                              ),
                            ),
                            _OpenButton(
                              key: ValueKey('open-${m.id}'),
                              label: l.listenThis(m.name),
                              icon: Icons.play_arrow_rounded,
                              onOpen: () => _openFromExplorer(context, m),
                            ),
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
    final icon = Icon(_iconOf(e), size: 44, color: e.dir ? c.accent : c.muted);
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
                  padding: const EdgeInsets.fromLTRB(8, 6, 4, 6),
                  child: Row(
                    children: [
                      SizedBox.square(
                        dimension: 56,
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
                            Text(e.name, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w700)),
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

/// The list layout's rows each have their height: a long name shows in full, on as many lines as
/// it needs (real test of 22 November 2026). Measured once per name, width and text size, so the
/// list knows every row's top without building it (a jump, a layout change, keep their place).
class _RowHeights {
  Object? _key;
  final _byId = <int, double>{};
  List<double> tops = const [0];

  /// The height of row [i], once measured.
  double? extent(int i) => i + 1 < tops.length ? tops[i + 1] - tops[i] : null;

  void measure(BuildContext context, List<MediaEntry> items, double width) {
    final scaler = MediaQuery.textScalerOf(context);
    final base = DefaultTextStyle.of(context).style;
    final direction = Directionality.of(context);
    final key = (width, scaler, base, direction);
    if (key != _key) {
      _key = key;
      _byId.clear();
    }
    final nameWidth = math.max(40.0, width - _MediaRow.besideName + 16);
    final detail = TextPainter(text: TextSpan(text: '0', style: base.merge(_MediaRow.detailStyle)), textDirection: direction, textScaler: scaler, maxLines: 1)
      ..layout();
    final detailHeight = detail.height;
    detail.dispose();
    final nameStyle = base.merge(_MediaRow.nameStyle);
    final out = List<double>.filled(items.length + 1, 0);
    for (var i = 0; i < items.length; i++) {
      final m = items[i];
      final h = _byId[m.id] ??= () {
        final name = TextPainter(text: TextSpan(text: m.name, style: nameStyle), textDirection: direction, textScaler: scaler)..layout(maxWidth: nameWidth);
        final h = _MediaRow.heightFor(name.height + detailHeight);
        name.dispose();
        return h;
      }();
      out[i + 1] = out[i] + h;
    }
    tops = out;
  }
}

/// A photo's tile as it flies to the viewer and back: the tile's own image, cropped to the rect
/// as it grows into the photo's shape (videos open their player without it).
class _HeroOf extends StatelessWidget {
  const _HeroOf({required this.entry, required this.child});

  final MediaEntry entry;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    if (entry.kind != MediaKind.images) return child;
    return Hero(
      tag: mediaHeroTag(entry),
      createRectTween: mediaRectTween, // closing: the flight lands here, on its curve
      // Closing, the viewer's own flies back: its photo whole, which this tile may show cropped
      // (beyond the mosaic's proportions). Opening, Flutter takes the viewer's by itself.
      flightShuttleBuilder: (context, animation, direction, from, to) {
        final viewer = direction == HeroFlightDirection.pop ? (from.widget as Hero).flightShuttleBuilder : null;
        if (viewer != null) return viewer(context, animation, direction, from, to);
        return ((direction == HeroFlightDirection.push ? from : to).widget as Hero).child;
      },
      child: child,
    );
  }
}

/// Opens the player of [m], among the media of its kind as the explorer holds them.
void _openFromExplorer(BuildContext context, MediaEntry m) {
  final items = AppScope.of(context, listen: false).state.explorer.media(m.kind).items;
  var i = items.indexWhere((x) => identical(x, m));
  if (i < 0) i = items.indexWhere((x) => x.id == m.id);
  // The grid brings the photo the viewer closes on into view, for it to fly back into its tile.
  final grid = context.findAncestorStateOfType<_MediaGridState>();
  if (i >= 0) unawaited(openMedia(context, List.of(items), i, onLeave: grid?._reveal));
}

/// ⤢ (▶ for a song): opens the player; 44 px to touch, a 30 px round on a picture.
class _OpenButton extends StatelessWidget {
  const _OpenButton({super.key, required this.label, required this.onOpen, this.icon = Icons.open_in_full_rounded, this.onImage = false});

  final String label;
  final VoidCallback onOpen;
  final IconData icon;

  /// Over a photo: white on a dark round, readable on any picture.
  final bool onImage;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Semantics(
      button: true,
      label: label,
      excludeSemantics: true,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onOpen,
        child: SizedBox.square(
          dimension: 44,
          child: Center(
            child: Container(
              width: onImage ? 30 : 36,
              height: onImage ? 30 : 36,
              decoration: onImage
                  ? BoxDecoration(color: Colors.black.withValues(alpha: 0.55), shape: BoxShape.circle, border: Border.all(color: Colors.white.withValues(alpha: 0.85), width: 1.5))
                  : BoxDecoration(color: c.surface, shape: BoxShape.circle),
              child: Icon(icon, size: onImage ? 16 : 20, color: onImage ? Colors.white : c.accent),
            ),
          ),
        ),
      ),
    );
  }
}

/// The room the floating bar of the home screen takes at the bottom of a list.
double bottomRoom(BuildContext context) => MediaQuery.paddingOf(context).bottom + 16;
