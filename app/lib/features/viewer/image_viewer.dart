/// The images in full (WP-8.2, mockup 20): pinched to zoom, swiped to the next, a touch hides the
/// commands; Flutter alone ([InteractiveViewer]), no library more. The photo is read where it is.
///
/// Quick, as a phone's gallery (real test of 23 November 2026: photos and strip took seconds):
/// the sharp thumbnail made ahead shows at once (read in ~1 ms); the photo is decoded at the
/// screen's size only (1.5×), its neighbours ahead; the whole of it once zoomed. Decoded at twice
/// the screen, a 12 Mpx photo held Flutter's decoder for long, and every thumbnail behind it.
library;

import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter/material.dart';

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
  const ImageViewer({super.key, required this.items, required this.index});

  final List<MediaEntry> items;
  final int index;

  @override
  State<ImageViewer> createState() => _ImageViewerState();
}

class _ImageViewerState extends State<ImageViewer> {
  late final PageController _pages = PageController(initialPage: widget.index);
  late int _index = widget.index;
  bool _chrome = true;
  bool _zoomed = false;
  bool _info = false; // the details line, as « Infos » shows it

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
    _explorer = AppScope.of(context, listen: false).state.explorer..addListener(_grown);
    _strip(_index);
    _ahead(_index);
    _more(_index);
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
      child: Scaffold(
        backgroundColor: Colors.black,
        body: Stack(
          children: [
            PageView.builder(
              key: const Key('image-pages'),
              controller: _pages,
              physics: _zoomed ? const NeverScrollableScrollPhysics() : const PageScrollPhysics(),
              itemCount: _items.length,
              onPageChanged: (i) {
                setState(() {
                  _index = i;
                  _zoomed = false;
                });
                _settled(i);
              },
              itemBuilder: (context, i) => _ZoomablePhoto(
                key: ValueKey('photo-${_items[i].id}'),
                entry: _items[i],
                onZoom: (z) {
                  if (z != _zoomed) setState(() => _zoomed = z);
                },
                onTap: () => setState(() => _chrome = !_chrome),
              ),
            ),
            if (_chrome) ...[
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
            ],
          ],
        ),
      ),
    );
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
  const _ZoomablePhoto({super.key, required this.entry, required this.onZoom, required this.onTap});

  final MediaEntry entry;
  final ValueChanged<bool> onZoom;
  final VoidCallback onTap;

  /// How long a photo stays before it is decoded: a swipe running through stays less.
  static const settleAfter = Duration(milliseconds: 150);

  @override
  State<_ZoomablePhoto> createState() => _ZoomablePhotoState();
}

class _ZoomablePhotoState extends State<_ZoomablePhoto> with SingleTickerProviderStateMixin {
  final _zoom = TransformationController();
  late final AnimationController _anim = AnimationController(vsync: this, duration: const Duration(milliseconds: 200))
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
        }
      }),
    );
  }

  @override
  void dispose() {
    _settle?.cancel();
    for (final key in _pending) {
      _explorer.forget(key); // passed by a swipe: not read for nothing
    }
    _anim.dispose();
    _zoom.dispose();
    super.dispose();
  }

  bool get _isZoomed => _zoom.value.getMaxScaleOnAxis() > 1.01;

  void _zoomed(bool z) {
    if (z && !_whole) setState(() => _whole = true);
    widget.onZoom(z);
  }

  void _doubleTap() {
    _from = _zoom.value.clone();
    if (_isZoomed) {
      _to = Matrix4.identity();
    } else {
      const s = 2.5;
      _to = Matrix4.identity()
        ..translateByDouble(-_tapAt.dx * (s - 1), -_tapAt.dy * (s - 1), 0, 1)
        ..scaleByDouble(s, s, 1, 1);
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
    return GestureDetector(
      onTap: widget.onTap,
      onDoubleTapDown: (d) => _tapAt = d.localPosition,
      onDoubleTap: _doubleTap,
      child: InteractiveViewer(
        transformationController: _zoom,
        maxScale: 5,
        onInteractionEnd: (_) => _zoomed(_isZoomed),
        // Two layers, each kept as it is: the thumbnail always under, the photo over it once
        // decoded. One image swapped for the other went black for 2 or 3 frames at the end of a
        // swipe (filmed at 60 frames a second, 23 November 2026).
        child: Stack(
          fit: StackFit.expand,
          children: [
            KeyedSubtree(key: const ValueKey('thumb'), child: placeholder),
            if (_decode)
              KeyedSubtree(
                key: const ValueKey('photo'),
                child: Image(
                  image: _photoAt(context, m, _whole ? 3 : 1.5),
                  fit: BoxFit.contain,
                  gaplessPlayback: true, // the screen-sized one stays until the whole is decoded
                  filterQuality: FilterQuality.medium,
                  semanticLabel: m.name,
                  frameBuilder: (context, child, frame, sync) => frame == null && !sync ? const SizedBox.shrink() : child,
                  errorBuilder: (context, error, stack) => const SizedBox.shrink(),
                ),
              ),
          ],
        ),
      ),
    );
  }
}
