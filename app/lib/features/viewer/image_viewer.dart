/// The images in full (WP-8.2, mockup 20): pinched to zoom, swiped to the next, a touch hides the
/// commands; Flutter alone ([InteractiveViewer]), no library more. The photo is read where it is.
///
/// Quick, as a phone's gallery (real test of 23 November 2026: photos and strip took seconds):
/// the sharp thumbnail made ahead shows at once (read in ~1 ms); the photo is decoded at the
/// screen's size only (1.5×), its neighbours ahead; the whole of it once zoomed. Decoded at twice
/// the screen, a 12 Mpx photo held Flutter's decoder for long, and every thumbnail behind it.
library;

import 'dart:async';
import 'dart:math' as math;
import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/foundation.dart' show ValueListenable;
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart' show RenderImage;
import 'package:flutter/services.dart';

import '../../app.dart';
import '../../l10n/app_localizations.dart';
import '../../library/explorer.dart';
import '../../platform/library.dart';
import '../../theme.dart';
import '../../widgets/v4.dart';
import '../explorer/explorer_tabs.dart' show LibraryImage, gridDecodePx, sharpSize;
import '../explorer/grid_layout.dart' show GridLayout;
import 'open_media.dart';

/// A thumbnail as the grid and the strip decode it ([LibraryImage] at [gridDecodePx]): the same
/// key in Flutter's image cache, so a photo shows its thumbnail at once, already decoded (a
/// fast swipe showed black pages: decoded again at another size, real test of 23 November 2026,
/// where a phone's gallery always shows a picture).
ImageProvider _thumbOf(BuildContext context, Uint8List bytes) => ResizeImage(MemoryImage(bytes), width: gridDecodePx(context));

/// [m] decoded to fit [scale] × the screen: the same key for the page and its preloading.
ImageProvider _photoAt(BuildContext context, MediaEntry m, double scale) {
  final size = MediaQuery.sizeOf(context);
  final dpr = MediaQuery.devicePixelRatioOf(context);
  return ResizeImage(
    FileImage(File(m.path)),
    width: (size.width * dpr * scale).round(),
    height: (size.height * dpr * scale).round(),
    policy: ResizeImagePolicy.fit,
  );
}

class ImageViewer extends StatefulWidget {
  const ImageViewer({super.key, required this.items, required this.index, this.onLeave});

  final List<MediaEntry> items;
  final int index;

  /// The photo shown as the viewer closes: the grid brings its tile into view, for the photo to
  /// fly back into it (swiped far from where it opened, real test of 23 November 2026).
  final ValueChanged<MediaEntry>? onLeave;

  @override
  State<ImageViewer> createState() => _ImageViewerState();
}

class _ImageViewerState extends State<ImageViewer> {
  late final PageController _pages = PageController(initialPage: widget.index);
  late int _index = widget.index;
  bool _chrome = true; // shown or hidden by a touch
  bool _zoomed = false; // zoomed in: the pages held still (set once a gesture ends)
  bool _zooming = false; // zooming in: the controls hidden, as a phone's gallery does
  bool _info = false; // the details line, as « Infos » shows it
  bool _landed = false; // the photo has flown in from its tile: the controls come after it
  bool _leaving = false; // flying back to its tile: the pages hold still, the photo alone shown
  Animation<double>? _route;
  final _fingers = <int>{}; // on the photo: two, and the pages hold still for the pinch
  final _edge = <int>{}; // set down at a side, where Android's back gesture starts: no swipe

  bool _primed = false;
  Timer? _settle;

  // The photos, as far as the explorer has loaded them: more come as the end nears (it stopped
  // at the first 300, real test of 23 November 2026).
  late List<MediaEntry> _items = widget.items;
  late Explorer _explorer;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_primed) return;
    _primed = true;
    // The controls come once the photo has landed. Flying over them, it hid the strip and the
    // arrows, which all came at once as it landed: the photo seemed to bounce back (filmed at
    // 60 frames a second, 23 November 2026).
    _route = ModalRoute.of(context)?.animation?..addStatusListener(_landing);
    _landed = _route?.isCompleted ?? true;
    _explorer = AppScope.of(context, listen: false).state.explorer..addListener(_grown);
    _strip(_index);
    _ahead(_index);
    _more(_index);
  }

  /// Landed: the controls come in; leaving: they go at once, the photo flying back over the grid.
  void _landing(AnimationStatus status) {
    final landed = status == AnimationStatus.completed;
    final leaving = status == AnimationStatus.reverse || status == AnimationStatus.dismissed;
    if ((landed != _landed || leaving != _leaving) && mounted) {
      setState(() {
        _landed = landed;
        _leaving = leaving;
      });
    }
  }

  /// Closing: the pages back on the photo shown, and held still. Android's back gesture, drawn
  /// from the right side, also swiped the pages: the next photo stayed over the grid while the
  /// other flew back, then vanished at once (filmed at 60 frames a second, 23 November 2026).
  void _leave() {
    if (_pages.hasClients && _pages.page != _index.toDouble()) _pages.jumpToPage(_index);
    widget.onLeave?.call(_items[_index]);
  }

  /// A finger down or up on the photo. Two: the pages hold still at once, for the pinch. Android
  /// starts a swipe after 8 dp, Flutter a pinch after 18: the swipe took the pinch on the phone
  /// (real test of 23 November 2026), as it does a pager's unless the photo claims it.
  /// Set down at a side, where Android's back gesture starts: no swipe either (see [_leave]).
  void _finger(PointerEvent e, {required bool down}) {
    final before = (_fingers.length > 1, _edge.isNotEmpty);
    if (down) {
      _fingers.add(e.pointer);
      final sides = MediaQuery.systemGestureInsetsOf(context);
      final width = MediaQuery.sizeOf(context).width;
      if (e.position.dx < sides.left || e.position.dx > width - sides.right) _edge.add(e.pointer);
    } else {
      _fingers.remove(e.pointer);
      _edge.remove(e.pointer);
    }
    if ((_fingers.length > 1, _edge.isNotEmpty) != before) setState(() {});
  }

  /// The explorer loaded further: the same photos, and more after them.
  void _grown() {
    if (_items.isEmpty) return;
    final list = _explorer.media(_items.first.kind).items;
    if (list.length > _items.length && identical(list.first, _items.first)) setState(() => _items = List.of(list));
  }

  void _more(int i) {
    if (_items.isNotEmpty && i >= _items.length - 40) unawaited(_explorer.more(_items.first.kind));
  }

  /// Once a photo stays (no swipe running through): its neighbours decoded before a swipe
  /// brings them. Not for each photo a long swipe passes: their decoding held up the others.
  void _settled(int i) {
    _settle?.cancel();
    _strip(i);
    _more(i);
    _settle = Timer(_ZoomablePhoto.settleAfter, () {
      if (mounted && i == _index) _ahead(i);
    });
  }

  void _ahead(int i) {
    for (final j in [i + 1, i - 1]) {
      if (j >= 0 && j < _items.length) unawaited(precacheImage(_photoAt(context, _items[j], 1.5), context, onError: (_, _) {}));
    }
  }

  /// The strip's thumbnails around [i], loaded before a long swipe reaches them (they showed
  /// black, real test of 23 November 2026): Android's own, quick to read, after what is on
  /// screen.
  void _strip(int i) {
    final state = AppScope.of(context, listen: false).state;
    final px = gridDecodePx(context);
    for (var d = 1; d <= stripAhead; d++) {
      for (final j in [i + d, i - d]) {
        if (j < 0 || j >= _items.length) continue;
        final m = _items[j];
        final key = '${m.kind.name}:${m.id}';
        // The nearest decoded too, as the page will show them: a swipe finds them ready.
        void decode(Uint8List? bytes) {
          if (bytes != null && d <= decodeAhead && mounted) unawaited(precacheImage(_thumbOf(context, bytes), context, onError: (_, _) {}));
        }

        final bytes = state.explorer.cached(key);
        if (bytes != null) {
          decode(bytes);
        } else {
          unawaited(state.explorer.image(key, () => state.library.thumbnail(m.kind, m.id, px), low: true).then(decode));
        }
      }
    }
  }

  /// How many thumbnails either side are decoded ahead, beyond loaded.
  static const decodeAhead = 6;

  /// How many thumbnails either side the strip loads ahead.
  static const stripAhead = 24;

  @override
  void dispose() {
    _settle?.cancel();
    _route?.removeStatusListener(_landing);
    if (_primed) _explorer.removeListener(_grown);
    _pages.dispose();
    super.dispose();
  }

  void _go(int i) {
    if (i < 0 || i >= _items.length) return;
    unawaited(_pages.animateToPage(i, duration: const Duration(milliseconds: 220), curve: Curves.easeOutCubic));
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final m = _items[_index];
    return Theme(
      data: ThemeData.dark(useMaterial3: true).copyWith(colorScheme: ColorScheme.dark(primary: c.accent)),
      // Transparent: the black behind is the route's, gone at once when it closes, the grid
      // seen behind the photo flying back to its tile (openMedia).
      child: PopScope(
        onPopInvokedWithResult: (didPop, _) {
          if (didPop) _leave();
        },
        child: Scaffold(
        backgroundColor: Colors.transparent,
        body: Stack(
          children: [
            Listener(
              onPointerDown: (e) => _finger(e, down: true),
              onPointerUp: (e) => _finger(e, down: false),
              onPointerCancel: (e) => _finger(e, down: false),
              child: PageView.builder(
              key: const Key('image-pages'),
              controller: _pages,
              physics: _zoomed || _fingers.length > 1 || _edge.isNotEmpty || _leaving ? const NeverScrollableScrollPhysics() : const PageScrollPhysics(),
              itemCount: _items.length,
              onPageChanged: (i) {
                setState(() {
                  _index = i;
                  _zoomed = false;
                });
                _settled(i);
              },
              // Leaving, the photo flying back alone: no other page over the grid.
              itemBuilder: (context, i) => Opacity(
                opacity: _leaving && i != _index ? 0 : 1,
                child: _ZoomablePhoto(
                key: ValueKey('photo-${_items[i].id}'),
                entry: _items[i],
                hero: i == _index,
                // The controls go as soon as a pinch zooms in; the pages stop following the
                // finger only once it lifts (changed during the pinch, it was lost, 23 November).
                onZooming: () {
                  if (!_zooming) setState(() => _zooming = true);
                },
                onZoom: (z) {
                  if (z != _zoomed || z != _zooming) {
                    setState(() {
                      _zoomed = z;
                      _zooming = z;
                    });
                  }
                },
                onTap: () => setState(() => _chrome = !_chrome),
                ),
              ),
            ),
            ),
            ..._controls(context, [
              Positioned(
                left: 0,
                right: 0,
                top: 0,
                child: DecoratedBox(
                  decoration: const BoxDecoration(gradient: LinearGradient(begin: Alignment.topCenter, end: Alignment.bottomCenter, colors: [Color(0xC0000000), Color(0x00000000)])),
                  child: SafeArea(
                    bottom: false,
                    child: PlayerHeader(entry: m, line: l.positionOf(_index + 1, _items.length)),
                  ),
                ),
              ),
              if (_index > 0)
                Positioned(left: 6, top: 0, bottom: 0, child: Center(child: _RoundButton(key: const Key('image-prev'), icon: Icons.chevron_left_rounded, label: l.previousItem, onTap: () => _go(_index - 1)))),
              if (_index < _items.length - 1)
                Positioned(right: 6, top: 0, bottom: 0, child: Center(child: _RoundButton(key: const Key('image-next'), icon: Icons.chevron_right_rounded, label: l.nextItem, onTap: () => _go(_index + 1)))),
              Positioned(
                left: 0,
                right: 0,
                bottom: 0,
                child: DecoratedBox(
                  decoration: const BoxDecoration(gradient: LinearGradient(begin: Alignment.topCenter, end: Alignment.bottomCenter, colors: [Color(0x00000000), Color(0xD9000000)])),
                  child: SafeArea(
                    top: false,
                    child: Padding(
                      padding: const EdgeInsets.fromLTRB(0, 28, 0, 10),
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          if (_info)
                            Padding(
                              padding: const EdgeInsets.fromLTRB(14, 0, 14, 10),
                              child: Text(mediaDetails(context, m), key: const Key('image-info'), style: const TextStyle(color: Colors.white70, fontSize: 12)),
                            ),
                          _Strip(items: _items, pages: _pages, index: _index, accent: c.accent),
                          const SizedBox(height: 10),
                          Center(child: _Actions(entry: m, accent: c.accent, info: _info, onInfo: () => setState(() => _info = !_info))),
                        ],
                      ),
                    ),
                  ),
                ),
              ),
            ]),
          ],
        ),
      ),
      ),
    );
  }

  /// The controls: hidden by a touch, or while zoomed in (both in 0.15 s); come in once the photo
  /// has flown from its tile, and go at once as it flies back. The system's bars stay as they
  /// are: hidden and shown, they changed what the app was given under the photo.
  List<Widget> _controls(BuildContext context, List<Widget> children) {
    final shown = _chrome && !_zooming && _landed;
    return [
      for (final child in children)
        if (child is Positioned)
          Positioned(
            left: child.left,
            top: child.top,
            right: child.right,
            bottom: child.bottom,
            width: child.width,
            height: child.height,
            child: IgnorePointer(
              ignoring: !shown,
              child: AnimatedOpacity(opacity: shown ? 1 : 0, duration: _landed ? const Duration(milliseconds: 150) : Duration.zero, child: child.child),
            ),
          ),
    ];
  }
}

class _RoundButton extends StatelessWidget {
  const _RoundButton({super.key, required this.icon, required this.label, required this.onTap});

  final IconData icon;
  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) => IconButton(
    tooltip: label,
    style: IconButton.styleFrom(backgroundColor: Colors.black54, foregroundColor: Colors.white, minimumSize: const Size(44, 44)),
    icon: Icon(icon, size: 26),
    onPressed: onTap,
  );
}

/// The photos side by side across the screen, as a phone's gallery shows them under a photo
/// (its video of 23 November 2026): the one shown in the middle, outlined; it follows a swipe,
/// and a finger along it runs through the photos, then it settles on the nearest.
class _Strip extends StatefulWidget {
  const _Strip({required this.items, required this.pages, required this.index, required this.accent});

  final List<MediaEntry> items;
  final PageController pages;
  final int index;
  final Color accent;

  @override
  State<_Strip> createState() => _StripState();
}

class _StripState extends State<_Strip> {
  // Each place: a thumbnail of 44 px, 4 px between; the one shown larger, outlined.
  static const _w = 48.0, _h = 60.0;
  late final ScrollController _strip = ScrollController(initialScrollOffset: widget.index * _w);
  bool _dragging = false; // the finger runs along the strip: it leads the photos

  @override
  void initState() {
    super.initState();
    widget.pages.addListener(_follow);
  }

  @override
  void dispose() {
    widget.pages.removeListener(_follow);
    _strip.dispose();
    super.dispose();
  }

  /// The photos swiped: the strip goes with them, the one shown in the middle.
  void _follow() {
    if (_dragging || !_strip.hasClients || !widget.pages.hasClients) return;
    final page = widget.pages.page;
    if (page != null) _strip.jumpTo((page * _w).clamp(0, _strip.position.maxScrollExtent).toDouble());
  }

  int _at(double offset) => (offset / _w).round().clamp(0, widget.items.length - 1);

  bool _scrolled(ScrollNotification n) {
    if (n is ScrollStartNotification && n.dragDetails != null) _dragging = true;
    if (!_dragging) return false;
    if (n is ScrollUpdateNotification) {
      final i = _at(n.metrics.pixels);
      if (widget.pages.hasClients && (widget.pages.page ?? -1).round() != i) widget.pages.jumpToPage(i);
    } else if (n is ScrollEndNotification) {
      _dragging = false;
      final i = _at(n.metrics.pixels);
      // The strip settles on a thumbnail by itself ([_SnapPhysics]): the photo goes with it.
      if (widget.pages.hasClients) widget.pages.jumpToPage(i);
    }
    return false;
  }

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    return SizedBox(
      key: const Key('image-strip'),
      height: _h,
      child: LayoutBuilder(
        builder: (context, box) => NotificationListener<ScrollNotification>(
          onNotification: _scrolled,
          child: ListView.builder(
            controller: _strip,
            physics: const _SnapPhysics(extent: _w),
            scrollDirection: Axis.horizontal,
            padding: EdgeInsets.symmetric(horizontal: (box.maxWidth - _w) / 2),
            itemExtent: _w,
            itemCount: widget.items.length,
            itemBuilder: (context, i) {
              final m = widget.items[i];
              final current = i == widget.index;
              return GestureDetector(
                key: ValueKey('strip-${m.id}'),
                onTap: () => unawaited(widget.pages.animateToPage(i, duration: const Duration(milliseconds: 220), curve: Curves.easeOutCubic)),
                child: Center(
                  child: AnimatedContainer(
                    duration: const Duration(milliseconds: 140),
                    width: current ? 46 : 40,
                    height: current ? 58 : 48,
                    decoration: BoxDecoration(
                      borderRadius: BorderRadius.circular(current ? 9 : 7),
                      border: Border.all(color: current ? widget.accent : Colors.transparent, width: 2),
                      boxShadow: current ? const [BoxShadow(color: Colors.black54, blurRadius: 8)] : null,
                    ),
                    child: ClipRRect(
                      borderRadius: BorderRadius.circular(current ? 7 : 7),
                      child: AnimatedOpacity(
                        duration: const Duration(milliseconds: 140),
                        opacity: current ? 1 : 0.7,
                        child: LibraryImage(
                          cacheKey: '${m.kind.name}:${m.id}',
                          decodePx: gridDecodePx(context), // decoded as the grid and the page decode them: once for all
                          load: (_) => state.library.thumbnail(m.kind, m.id, gridDecodePx(context)),
                        ),
                      ),
                    ),
                  ),
                ),
              );
            },
          ),
        ),
      ),
    );
  }
}

/// The strip's scroll, settling on a thumbnail: a fling runs on a few, then stops on one (a free
/// scroll felt loose, real test of 23 November 2026).
class _SnapPhysics extends ScrollPhysics {
  const _SnapPhysics({required this.extent, super.parent});

  final double extent;

  @override
  _SnapPhysics applyTo(ScrollPhysics? ancestor) => _SnapPhysics(extent: extent, parent: buildParent(ancestor));

  @override
  Simulation? createBallisticSimulation(ScrollMetrics position, double velocity) {
    if ((velocity <= 0 && position.pixels <= position.minScrollExtent) || (velocity >= 0 && position.pixels >= position.maxScrollExtent)) {
      return super.createBallisticSimulation(position, velocity);
    }
    final tolerance = toleranceFor(position);
    final carried = position.pixels + velocity * 0.18; // a fling carries on a few thumbnails
    final target = ((carried / extent).round() * extent).clamp(position.minScrollExtent, position.maxScrollExtent).toDouble();
    if ((target - position.pixels).abs() < tolerance.distance && velocity.abs() < tolerance.velocity) return null;
    return ScrollSpringSimulation(spring, position.pixels, target, velocity, tolerance: tolerance);
  }
}

/// The actions, in a round bar floating over the photo (as a phone's gallery has them): pick
/// it, its details, share it.
class _Actions extends StatelessWidget {
  const _Actions({required this.entry, required this.accent, required this.info, required this.onInfo});

  final MediaEntry entry;
  final Color accent;
  final bool info;
  final VoidCallback onInfo;

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final sel = state.selection;
    final pick = Explorer.pickMedia(entry);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 4),
      decoration: BoxDecoration(color: const Color(0xE6242826), borderRadius: BorderRadius.circular(28)),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Flexible(
            child: ListenableBuilder(
            listenable: sel,
            builder: (context, _) {
              final on = sel.contains(pick.path);
              return Semantics(
                button: true,
                toggled: on,
                child: InkWell(
                  key: const Key('media-select'),
                  borderRadius: BorderRadius.circular(22),
                  onTap: () => sel.toggle(pick),
                  child: ConstrainedBox(
                    constraints: const BoxConstraints(minHeight: 44),
                    child: Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 12),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          PickBox(on: on, onImage: true, size: 22),
                          const SizedBox(width: 8),
                          Flexible(
                            child: Text(
                              on ? l.selectedWith(l.itemsCount(sel.count)) : l.selectThis,
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                              style: TextStyle(fontWeight: FontWeight.w800, fontSize: 14, color: on ? accent : Colors.white),
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
              );
            },
          ),
          ),
          IconButton(
            key: const Key('image-info-toggle'),
            tooltip: l.mediaInfo,
            isSelected: info,
            color: Colors.white,
            selectedIcon: Icon(Icons.info_rounded, color: accent),
            icon: const Icon(Icons.info_outline_rounded),
            onPressed: onInfo,
          ),
          if (state.openWith.supported)
            IconButton(
              key: const Key('image-share'),
              tooltip: l.shareMedia,
              color: Colors.white,
              icon: const Icon(Icons.share_rounded),
              onPressed: () => unawaited(state.openWith.share(entry)),
            ),
        ],
      ),
    );
  }
}

/// One photo: pinched or double-tapped to zoom (to 5×). At once, what is at hand: its sharp
/// thumbnail, else Android's; once it has stayed [settleAfter] (a long swipe passing through
/// does not), the sharp one read from the disk and the photo at the screen's size; zoomed in,
/// the whole of it.
class _ZoomablePhoto extends StatefulWidget {
  const _ZoomablePhoto({super.key, required this.entry, required this.onZoom, required this.onZooming, required this.onTap, this.hero = false});

  final MediaEntry entry;
  final ValueChanged<bool> onZoom;

  /// A pinch is zooming in (said at each move): the controls go.
  final VoidCallback onZooming;
  final VoidCallback onTap;

  /// The photo shown: it flies from its tile when the viewer opens, back to it when it closes.
  final bool hero;

  /// How long a photo stays before it is decoded: a swipe running through stays less.
  static const settleAfter = Duration(milliseconds: 150);

  @override
  State<_ZoomablePhoto> createState() => _ZoomablePhotoState();
}

class _ZoomablePhotoState extends State<_ZoomablePhoto> with SingleTickerProviderStateMixin {
  final _zoom = TransformationController();
  // A double tap zooms in 0.28 s, as a phone's gallery does (filmed on 23 November 2026).
  late final AnimationController _anim = AnimationController(vsync: this, duration: const Duration(milliseconds: 280))
    ..addListener(() {
      final a = _from;
      final b = _to;
      if (a != null && b != null) _zoom.value = Matrix4Tween(begin: a, end: b).transform(Curves.easeOutCubic.transform(_anim.value));
    });
  Matrix4? _from, _to;
  Offset _tapAt = Offset.zero;
  Uint8List? _first; // shown until the photo is decoded
  bool _sharpFirst = false;
  bool _decode = false; // stayed long enough: the photo itself
  bool _whole = false; // zoomed in once: the photo at three times the screen
  bool _asked = false;
  Timer? _settle;
  final _pending = <String>[]; // asked of the explorer, not come yet: dropped if it goes
  late Explorer _explorer;
  Size _view = Size.zero; // the screen given
  Rect _photo = Rect.zero; // where the photo lies in it, fitted
  bool _holding = false;
  // The photo whole, as shown here, for its flight to and from its tile ([_Flying]).
  final _flying = ValueNotifier<ImageProvider?>(null);
  double? _shownAt; // the photo itself shown, decoded at this scale: at hand

  @override
  void initState() {
    super.initState();
    _zoom.addListener(_hold);
  }

  /// The zoom held within the photo: never smaller than the screen fits it, never panned past its
  /// edges into the black around it; centred along a side it does not fill (filmed in a phone's
  /// gallery, 23 November 2026).
  void _hold() {
    if (_holding || _photo.isEmpty) return;
    final m = _zoom.value;
    final s = m.getMaxScaleOnAxis();
    final held = _held(s, m.getTranslation().x, m.getTranslation().y);
    if (held == null) return;
    _holding = true;
    _zoom.value = held;
    _holding = false;
  }

  /// The transform at scale [s] nearest (tx, ty) that keeps the photo inside its bounds; null when
  /// it already does.
  Matrix4? _held(double s, double tx, double ty) {
    double axis(double t, double lo, double size, double view) {
      final scaled = size * s;
      if (scaled <= view + 0.5) return (view - scaled) / 2 - lo * s; // centred
      return t.clamp(view - (lo + size) * s, -lo * s).toDouble();
    }

    final scale = s < 1 ? 1.0 : s;
    final nx = axis(tx, _photo.left, _photo.width, _view.width);
    final ny = axis(ty, _photo.top, _photo.height, _view.height);
    if ((nx - tx).abs() < 0.01 && (ny - ty).abs() < 0.01 && scale == s) return null;
    return Matrix4.identity()
      ..translateByDouble(nx, ny, 0, 1)
      ..scaleByDouble(scale, scale, 1, 1);
  }

  String get _key => '${widget.entry.kind.name}:${widget.entry.id}';

  /// The sharp thumbnail is the whole photo only within the mosaic's proportions: beyond (a
  /// screenshot, a panorama), it is cropped, and changed shape when the photo came.
  bool get _sharpWhole {
    final a = widget.entry.aspect;
    return a >= GridLayout.minAspect && a <= GridLayout.maxAspect;
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _explorer = AppScope.of(context, listen: false).state.explorer;
    if (_asked) return;
    _asked = true;
    final state = AppScope.of(context, listen: false).state;
    final sharp = _sharpWhole ? state.explorer.cached('$_key@sharp') : null;
    _sharpFirst = sharp != null;
    _first = sharp ?? state.explorer.cached(_key);
    _flyingNow();
    if (_first == null) _ask(_key, () => state.library.thumbnail(widget.entry.kind, widget.entry.id, gridDecodePx(context)), sharp: false);
    // Already decoded (a neighbour decoded ahead): at once; else once it has stayed.
    final photo = _photoAt(context, widget.entry, 1.5);
    unawaited(
      photo.obtainKey(createLocalImageConfiguration(context)).then((key) {
        if (!mounted) return;
        if (PaintingBinding.instance.imageCache.containsKey(key)) {
          setState(() => _decode = true);
        } else {
          _settle = Timer(_ZoomablePhoto.settleAfter, _stayed);
        }
      }),
    );
  }

  /// It stayed: the sharp thumbnail from the disk, and the photo itself.
  void _stayed() {
    if (!mounted) return;
    final state = AppScope.of(context, listen: false).state;
    if (!_sharpFirst && _sharpWhole) {
      final size = sharpSize(context);
      final m = widget.entry;
      _ask('$_key@sharp', () => state.library.sharpThumbnail(m, height: size.height, width: size.width, keptOnly: true), sharp: true);
    }
    setState(() => _decode = true);
  }

  void _ask(String key, Future<Uint8List?> Function() load, {required bool sharp}) {
    _pending.add(key);
    unawaited(
      _explorer.image(key, load).then((bytes) {
        _pending.remove(key);
        if (!mounted || bytes == null) return;
        if (sharp || !_sharpFirst) {
          setState(() {
            _first = bytes;
            _sharpFirst = _sharpFirst || sharp;
          });
          _flyingNow();
        }
      }),
    );
  }

  /// What flies between the tile and the screen: the photo whole, as shown here, the frame
  /// cropping it as it goes (cover). Its tile shows a very tall or very wide photo cropped
  /// (beyond the mosaic's proportions): flown, that crop grew larger than the photo, which then
  /// shrank by 6 % as it landed (filmed at 60 frames a second, 23 November 2026). Until the
  /// photo here is at hand, the tile's own image flies.
  void _flyingNow() {
    final first = _first;
    final at = _shownAt;
    _flying.value = at != null
        ? _photoAt(context, widget.entry, at)
        : first == null
        ? null
        : _sharpFirst
        ? MemoryImage(first)
        : _thumbOf(context, first);
  }

  @override
  void dispose() {
    _settle?.cancel();
    _flying.dispose();
    for (final key in _pending) {
      _explorer.forget(key); // passed by a swipe: not read for nothing
    }
    _anim.dispose();
    _zoom
      ..removeListener(_hold)
      ..dispose();
    super.dispose();
  }

  bool get _isZoomed => _zoom.value.getMaxScaleOnAxis() > 1.01;

  void _zoomed(bool z) {
    if (z && !_whole) setState(() => _whole = true);
    widget.onZoom(z);
  }

  /// Double tap: zoomed in around the finger (2.5×, or more if that is what fills the screen),
  /// or back to the whole photo.
  void _doubleTap() {
    _from = _zoom.value.clone();
    if (_isZoomed) {
      _to = Matrix4.identity();
    } else {
      final fill = _photo.isEmpty ? 1.0 : math.max(_view.width / _photo.width, _view.height / _photo.height);
      final s = math.min(4.0, math.max(2.5, fill));
      final tx = -_tapAt.dx * (s - 1), ty = -_tapAt.dy * (s - 1);
      _to = _held(s, tx, ty) ??
          (Matrix4.identity()
            ..translateByDouble(tx, ty, 0, 1)
            ..scaleByDouble(s, s, 1, 1));
    }
    _zoomed(!_isZoomed);
    unawaited(_anim.forward(from: 0));
  }

  @override
  Widget build(BuildContext context) {
    final m = widget.entry;
    final first = _first;
    final placeholder = first == null
        ? const SizedBox.expand()
        : Image(
            image: _sharpFirst ? MemoryImage(first) : _thumbOf(context, first),
            fit: BoxFit.contain,
            gaplessPlayback: true,
            filterQuality: FilterQuality.medium,
            excludeFromSemantics: true,
          );
    // Two layers, each kept as it is: the thumbnail always under, the photo over it once
    // decoded. One image swapped for the other went black for 2 or 3 frames at the end of a
    // swipe (filmed at 60 frames a second, 23 November 2026).
    final scale = _whole ? 3.0 : 1.5;
    final layers = Stack(
      fit: StackFit.expand,
      children: [
            KeyedSubtree(key: const ValueKey('thumb'), child: placeholder),
            if (_decode)
              KeyedSubtree(
                key: const ValueKey('photo'),
                child: Image(
                  image: _photoAt(context, m, scale),
                  fit: BoxFit.contain,
                  gaplessPlayback: true, // the screen-sized one stays until the whole is decoded
                  filterQuality: FilterQuality.medium,
                  semanticLabel: m.name,
                  // Nothing until its first frame; then always what it has, the one before
                  // while the next decodes (hidden then, the blurred thumbnail showed through).
                  frameBuilder: (context, child, frame, sync) {
                    if ((frame != null || sync) && _shownAt != scale) {
                      _shownAt = scale;
                      WidgetsBinding.instance.addPostFrameCallback((_) {
                        if (mounted) _flyingNow();
                      });
                    }
                    return _shownAt == null ? const SizedBox.shrink() : child;
                  },
                  errorBuilder: (context, error, stack) => const SizedBox.shrink(),
                ),
              ),
      ],
    );
    return LayoutBuilder(
      builder: (context, box) {
        _view = box.biggest;
        // The photo fitted to the screen, by its proportions (known from MediaStore): its rect is
        // what the zoom keeps within, and what flies from its tile.
        final a = m.aspect;
        final fitted = a > 0 ? applyBoxFit(BoxFit.contain, Size(a, 1), _view).destination : _view;
        _photo = Alignment.center.inscribe(fitted, Offset.zero & _view);
        return GestureDetector(
          onTap: widget.onTap,
          onDoubleTapDown: (d) => _tapAt = d.localPosition,
          onDoubleTap: _doubleTap,
          child: InteractiveViewer(
            transformationController: _zoom,
            minScale: 1, // never smaller than the screen fits it
            maxScale: 5,
            // The controls go at once as a pinch zooms in, and the whole photo starts loading.
            onInteractionUpdate: (_) {
              if (!_isZoomed) return;
              if (!_whole) setState(() => _whole = true);
              widget.onZooming();
            },
            onInteractionEnd: (_) => _zoomed(_isZoomed),
            child: Stack(
              children: [
                Positioned.fromRect(
                  rect: _photo,
                  child: HeroMode(
                    enabled: widget.hero,
                    child: Hero(
                      tag: mediaHeroTag(m),
                      createRectTween: mediaRectTween,
                      flightShuttleBuilder: (context, animation, direction, from, to) => _Flying(
                        key: const Key('photo-flying'),
                        whole: _flying,
                        tile: ((direction == HeroFlightDirection.push ? from : to).widget as Hero).child,
                        from: direction == HeroFlightDirection.push ? from : null,
                      ),
                      child: layers,
                    ),
                  ),
                ),
              ],
            ),
          ),
        );
      },
    );
  }
}

/// The photo flying between its tile and the screen: whole, cropped by the frame as it goes
/// (cover), which shows it as its tile does at one end and as the screen does at the other.
/// Until the whole photo is at hand, its tile's image; once shown, never the tile's again.
class _Flying extends StatefulWidget {
  const _Flying({super.key, required this.whole, required this.tile, this.from});

  final ValueListenable<ImageProvider?> whole;
  final Widget tile;

  /// Opening: the tile's hero, its image as painted there flying first.
  final BuildContext? from;

  @override
  State<_Flying> createState() => _FlyingState();
}

class _FlyingState extends State<_Flying> {
  bool _shown = false;
  ui.Image? _painted;

  @override
  void initState() {
    super.initState();
    final from = widget.from;
    if (from != null && from.mounted) _painted = _paintedIn(from.findRenderObject());
  }

  @override
  void dispose() {
    _painted?.dispose();
    super.dispose();
  }

  /// The tile as it was painted, from the very first frame. Its widget built again here decoded
  /// its image anew when Flutter's cache no longer held it: nothing flew for the first frames,
  /// the screen all black for a dark screenshot (filmed at 60 frames a second, 23 November 2026).
  Widget get _tile {
    final painted = _painted;
    return painted == null ? widget.tile : RawImage(image: painted, fit: BoxFit.cover, filterQuality: FilterQuality.medium);
  }

  @override
  Widget build(BuildContext context) => ValueListenableBuilder<ImageProvider?>(
    valueListenable: widget.whole,
    builder: (context, image, _) => image == null
        ? _tile
        : Image(
            image: image,
            fit: BoxFit.cover,
            gaplessPlayback: true,
            filterQuality: FilterQuality.medium,
            excludeFromSemantics: true,
            frameBuilder: (context, child, frame, sync) {
              if (frame != null || sync) _shown = true;
              return _shown ? child : _tile;
            },
          ),
  );
}

/// The first image painted under [root], as a copy its taker disposes of; null if none.
ui.Image? _paintedIn(RenderObject? root) {
  ui.Image? found;
  void visit(RenderObject r) {
    if (found != null) return;
    final image = r is RenderImage ? r.image : null;
    if (image != null) {
      found = image.clone();
      return;
    }
    r.visitChildren(visit);
  }

  if (root != null) visit(root);
  return found;
}
