/// The tabs of the explorer (WP-B.9, §1.2 of the J6b plan): Apps and Photos in grids, Videos,
/// Music, Files. A tap picks or unpicks; the basket of the home screen shows the selection.
/// Lists are lazy and the images decoded at the size shown (UI-1).
library;

import 'dart:async';
import 'dart:math' as math;

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
  const LibraryImage({super.key, required this.cacheKey, required this.load, this.fit = BoxFit.cover, this.placeholder});

  final String cacheKey;

  /// Loads the image at the size it is shown, in pixels: sharp, never stretched.
  final Future<Uint8List?> Function(int px) load;
  final BoxFit fit;

  /// Shown until the image comes, and instead of it if there is none: a plain tile otherwise.
  final Widget? placeholder;

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
        final px = (box.biggest.shortestSide * dpr).round().clamp(32, 1024);
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

/// While two fingers pinch a grid: the tabs do not page under them.
final explorerPinching = ValueNotifier<bool>(false);

class _MediaGridState extends State<_MediaGrid> with AutomaticKeepAliveClientMixin, SingleTickerProviderStateMixin {
  String? _album;
  ScrollController _scroll = ScrollController();

  // The pinch (plan J8, WP-8.1b, real test of 17 November): the grid follows the fingers,
  // around the point between them; past a step it takes the next number of columns, the tile
  // under the fingers kept there; scrolling waits while two fingers are down.
  final _points = <int, Offset>{};
  double? _startSpread;
  double _carry = 1;
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

  // As in a phone's gallery (Samsung's, seen on 17 November): between two numbers of columns,
  // the next grid fades in over the current one, both at the scale of the fingers and the tile
  // under them at the same place.
  int? _target;
  ScrollController? _targetScroll;
  double _targetOffset = 0;

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
    _scaleN.dispose();
    _scroll.dispose();
    _targetScroll?.dispose();
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

  /// The layout of [t] columns readied under the fingers: its scroll puts the same tile there.
  void _prepare(int? t) {
    final old = _targetScroll;
    _targetScroll = null;
    _target = t;
    if (old != null) WidgetsBinding.instance.addPostFrameCallback((_) => old.dispose());
    if (t == null || !_scroll.hasClients) return;
    final c = _shownColumns;
    final index = _at(_scroll.offset + _focal.dy, c, false, x: _focal.dx);
    final within = _scroll.offset + _focal.dy - _top(index, c, false);
    _targetOffset = (_top(index, t, false) + within * c / t - _focal.dy).clamp(0.0, double.infinity).toDouble();
    _targetScroll = ScrollController(initialScrollOffset: _targetOffset);
  }

  /// The layout faded in becomes the one shown, as it is: already built and placed under the
  /// fingers, it is neither rebuilt nor moved (real test of 17 November: rebuilt then moved a
  /// frame later, the tiles drifted step after step). Its number of columns counts at once: the
  /// next move of the fingers starts from it, never twice from the old one.
  void _commit(Explorer ex, int t) {
    final next = _targetScroll;
    if (next != null) {
      final old = _scroll;
      _scroll = next;
      WidgetsBinding.instance.addPostFrameCallback((_) => old.dispose());
    }
    _targetScroll = null;
    _target = null;
    _shownColumns = t;
    ex.setLayout(widget.kind, columns: t, asList: false);
  }

  void _settled() {
    final t = _target;
    if (t != null && _settleGoal != 1) {
      _commit(AppScope.of(context, listen: false).state.explorer, t);
      setState(() => _scale = 1);
    } else {
      setState(() => _prepare(null));
    }
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

  void _down(PointerDownEvent e, Explorer ex) {
    _points[e.pointer] = e.localPosition;
    if (_points.length == 2 && !ex.asList(widget.kind)) {
      _settle.stop();
      _startSpread = _spread();
      _carry = _scale;
      _focal = _between();
      explorerPinching.value = true;
      setState(() {}); // the list stops scrolling
    }
  }

  void _move(PointerMoveEvent e, Explorer ex) {
    if (!_points.containsKey(e.pointer)) return;
    _points[e.pointer] = e.localPosition;
    final start = _startSpread, now = _spread();
    if (start == null || now == null || start <= 0) return;
    final c = _shownColumns;
    var scale = now / start * _carry;
    // Spreading: fewer columns; pinching: more; none past the ends.
    final want = scale > 1 ? c - 1 : (scale < 1 ? c + 1 : null);
    final t = want != null && want >= Explorer.minColumns && want <= Explorer.maxColumns ? want : null;
    // Only a new target or a new layout rebuilds; the zoom itself does not.
    var rebuild = false;
    if (t != _target) {
      _prepare(t);
      rebuild = true;
    }
    if (t == null) {
      scale = 1; // at 2 or 5 columns, nothing further: the view itself never zooms
    } else if ((t < c && scale >= c / t) || (t > c && scale <= c / t)) {
      // The next grid reached: it becomes the current one, the gesture goes on from there.
      _commit(ex, t);
      _carry /= c / t;
      scale /= c / t;
      rebuild = true;
    }
    _scale = scale;
    if (rebuild) setState(() {});
  }

  void _up(PointerEvent e) {
    _points.remove(e.pointer);
    if (_points.length >= 2 || _startSpread == null) return;
    _startSpread = null;
    explorerPinching.value = false;
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
        final pinching = _points.length >= 2;
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
                    final align = _box.width > 0 && _box.height > 0
                        ? Alignment(_focal.dx / _box.width * 2 - 1, _focal.dy / _box.height * 2 - 1)
                        : Alignment.center;
                    Widget view(ScrollController controller, int cols, Key key, {bool frozen = false}) => CustomScrollView(
                      key: key,
                      controller: controller,
                      physics: frozen ? const NeverScrollableScrollPhysics() : null,
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
                    final next = _target, nextScroll = _targetScroll;
                    // Built once per layout: the zoom below only scales and fades them.
                    final current = IgnorePointer(
                      ignoring: false,
                      child: view(_scroll, columns, ValueKey('media-${widget.kind.name}'), frozen: pinching || next != null),
                    );
                    final upcoming = next != null && nextScroll != null
                        ? IgnorePointer(child: view(nextScroll, next, ValueKey('media-${widget.kind.name}'), frozen: true))
                        : null;
                    return Listener(
                      onPointerDown: (e) => _down(e, ex),
                      onPointerMove: (e) => _move(e, ex),
                      onPointerUp: _up,
                      onPointerCancel: _up,
                      child: ClipRect(
                        child: ValueListenableBuilder<double>(
                          valueListenable: _scaleN,
                          builder: (context, scale, _) {
                            final fade = _progress;
                            return Stack(
                              fit: StackFit.expand,
                              children: [
                                KeyedSubtree(
                                  key: ValueKey('layer-$columns'),
                                  child: Opacity(opacity: 1 - fade, child: Transform.scale(scale: scale, alignment: align, child: current)),
                                ),
                                // Built as soon as it is the target, faded in from 0: ready when reached.
                                if (upcoming != null)
                                  KeyedSubtree(
                                    key: ValueKey('layer-$next'),
                                    child: Opacity(opacity: fade, child: Transform.scale(scale: scale / (columns / next!), alignment: align, child: upcoming)),
                                  ),
                              ],
                            );
                          },
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

/// A day's name, and « Tout ce jour » with its square box.
class _DayHeader extends StatelessWidget {
  const _DayHeader({required this.day, required this.items});

  final DateTime day;
  final List<MediaEntry> items;

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final now = DateTime.now();
    final today = DateTime(now.year, now.month, now.day);
    final locale = Localizations.localeOf(context).toLanguageTag();
    final name = day == today
        ? l.today
        : day == today.subtract(const Duration(days: 1))
        ? l.yesterday
        : (day.year == today.year ? DateFormat.MMMMd(locale) : DateFormat.yMMMMd(locale)).format(day);
    final picks = [for (final m in items) Explorer.pickMedia(m)];
    final all = state.selection.allOf(picks);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 6, 6, 2),
      child: Row(
        children: [
          Expanded(child: Text(name, style: titleStyle(14))),
          BoxToggle(
            label: l.selectDay,
            semanticLabel: l.selectDayLabel(name),
            on: all,
            small: true,
            onPressed: () => state.selection.toggleAll(picks),
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

  @override
  Widget build(BuildContext context) {
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
            LibraryImage(cacheKey: '${kind.name}:${m.id}', load: (px) => state.library.thumbnail(kind, m.id, px)),
            if (on) DecoratedBox(decoration: BoxDecoration(border: Border.all(color: c.strong, width: columns >= 5 ? 2 : 3))),
            if (kind == MediaKind.video)
              Positioned(
                left: 4,
                bottom: 4,
                child: Container(
                  padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
                  decoration: BoxDecoration(color: Colors.black.withValues(alpha: 0.72), borderRadius: BorderRadius.circular(6)),
                  child: Text(
                    formatDuration(context, m.durationMs ~/ 1000),
                    style: TextStyle(color: Colors.white, fontSize: columns >= 4 ? 10 : 11, fontWeight: FontWeight.w700, fontFeatures: tabular),
                  ),
                ),
              ),
            Positioned(right: 5, top: 5, child: PickBox(on: on, onImage: true, size: columns >= 5 ? 18 : 22)),
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

  @override
  Widget build(BuildContext context) {
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
