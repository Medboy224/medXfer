/// The PDF reader (plan J8, WP-8.2, mockups 27 and 28 of the « Version 4 » canvas, agreed on
/// 24 November 2026): the pages one under the other at the screen's width; pinched or double-
/// tapped to zoom, the part shown drawn again sharp; a bar of pages to go far fast, a page by its
/// number, all the pages as thumbnails; « Sélectionner » as everywhere; another app for a PDF
/// locked by a password.
library;

import 'dart:async';
import 'dart:math' as math;
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app.dart';
import '../../l10n/app_localizations.dart';
import '../../media/pdf_document.dart';
import '../../platform/library.dart';
import '../../widgets/format.dart';
import 'open_media.dart';

const _ground = Color(0xFF252826);
const _bar = Color(0xF00D0F0E);
const _sheet = Color(0xFF151816);
const _muted = Color(0xFFC9CEC6);

/// Opens [file] in the PDF reader.
Future<void> openPdf(BuildContext context, FileEntry file) =>
    Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => PdfScreen(file: file)));

/// Whether [name] is a PDF.
bool isPdf(String name) => name.toLowerCase().endsWith('.pdf');

class PdfScreen extends StatefulWidget {
  const PdfScreen({super.key, required this.file});

  final FileEntry file;

  /// The zoom's bounds.
  static const maxScale = 5.0;

  @override
  State<PdfScreen> createState() => _PdfScreenState();
}

enum _Failure { protected, failed }

class _PdfScreenState extends State<PdfScreen> with SingleTickerProviderStateMixin {
  PdfDocument? _doc;
  _Failure? _failure;
  String _detail = '';
  _Renders? _pages; // the pages at the screen's width
  final _v = ScrollController();
  final _h = ScrollController();
  final _page = ValueNotifier<int>(0); // the page at the top third of the screen
  bool _chrome = true;

  // The zoom: [_scale] laid out; [_live] painted over it while a pinch or a double tap makes it
  // grow, the content point under [_from] following the fingers to [_to]. Shrinking, it is laid
  // out at each move instead ([_zoom]).
  double _scale = 1;
  double _live = 1;
  Offset _from = Offset.zero, _to = Offset.zero;
  final _fingers = <int, Offset>{};
  double? _spread0;
  double _spreadNow = 0;
  double _tapFrom = 1; // the scale a double tap started from
  Offset _mid0 = Offset.zero;
  bool _pinching = false;
  Offset _tapAt = Offset.zero;
  late final AnimationController _tapZoom = AnimationController(vsync: this, duration: const Duration(milliseconds: 240));
  double _tapGoal = 1;

  // Zoomed in: the part of each page on the screen, drawn at the scale shown.
  final _tiles = <int, _Tile>{};
  int _tileRun = 0;
  Timer? _sharpen;

  Size _view = Size.zero;
  double _lead = 0, _trail = 0; // fixed room above the first page and below the last (the bars)

  @override
  void initState() {
    super.initState();
    _v.addListener(_onScroll);
    _tapZoom.addListener(() {
      final t = Curves.easeOutCubic.transform(_tapZoom.value);
      _zoom(_tapFrom * (1 + (_tapGoal - 1) * t) / _scale);
    });
    _tapZoom.addStatusListener((s) {
      if (s == AnimationStatus.completed) _commit();
    });
    unawaited(_open());
  }

  Future<void> _open() async {
    final reader = AppScope.of(context, listen: false).state.pdf;
    try {
      final doc = await reader.open(widget.file.path);
      if (!mounted) return doc.close();
      setState(() => _doc = doc);
    } on PdfProtected {
      if (mounted) setState(() => _failure = _Failure.protected);
    } on Object catch (e) {
      if (mounted) {
        setState(() {
          _failure = _Failure.failed;
          _detail = '$e';
        });
      }
    }
  }

  @override
  void dispose() {
    _sharpen?.cancel();
    _tapZoom.dispose();
    _v.dispose();
    _h.dispose();
    _page.dispose();
    _pages?.dispose();
    for (final t in _tiles.values) {
      t.image.dispose();
    }
    _doc?.close();
    super.dispose();
  }

  // The layout at scale [s]: every length but the bars' room grows with the zoom.
  double _pageW(double s) => (_view.width - 16) * s;
  double _heightOf(int i, double s) {
    final p = _doc!.pages[i];
    return p.width <= 0 ? _pageW(s) * 1.414 : _pageW(s) * p.height / p.width;
  }

  /// The whole list's height at scale [s]: the bars' room, the pages and their gaps.
  double _contentHeight(double s) {
    final n = _doc!.pages.length;
    return _topOf(n - 1, s) + _heightOf(n - 1, s) + 8 * s + _trail;
  }

  double _topOf(int i, double s) {
    var y = _lead + 8 * s;
    for (var j = 0; j < i; j++) {
      y += _heightOf(j, s) + 8 * s;
    }
    return y;
  }

  void _onScroll() {
    final doc = _doc;
    if (doc == null || !_v.hasClients) return;
    final at = _v.offset + _view.height / 3;
    var y = _lead + 8 * _scale;
    var i = 0;
    for (; i < doc.pages.length - 1; i++) {
      y += _heightOf(i, _scale) + 8 * _scale;
      if (y > at) break;
    }
    _page.value = i;
  }

  void _jumpTo(int i) {
    if (!_v.hasClients) return;
    final top = _topOf(i, _scale) - _lead - 8 * _scale;
    _v.jumpTo(top.clamp(0.0, _v.position.maxScrollExtent));
    _page.value = i;
    _sharpenSoon();
  }

  // ── the zoom ───────────────────────────────────────────────────────────────────────────

  void _down(PointerDownEvent e) {
    _fingers[e.pointer] = e.localPosition;
    if (_fingers.length == 2 && !_tapZoom.isAnimating) {
      final p = _fingers.values.toList();
      _spread0 = (p[0] - p[1]).distance;
      _mid0 = (p[0] + p[1]) / 2;
      _from = _mid0;
      _to = _mid0;
      // The list stays still while two fingers pinch: the first one scrolled it.
      setState(() => _pinching = true);
    }
  }

  void _move(PointerMoveEvent e) {
    if (!_fingers.containsKey(e.pointer)) return;
    _fingers[e.pointer] = e.localPosition;
    final s0 = _spread0;
    if (s0 == null || _fingers.length < 2 || s0 < 1) return;
    final p = _fingers.values.toList();
    _spreadNow = (p[0] - p[1]).distance;
    _to = (p[0] + p[1]) / 2;
    _zoom((_spreadNow / s0).clamp(1 / _scale, PdfScreen.maxScale / _scale));
  }

  /// The zoom at [live] times the one laid out. Growing, painted: all it shows is already laid
  /// out and drawn. Shrinking, laid out at once: what comes into view around it was not (zoomed
  /// in then out at once, a small piece of page stayed alone in the dark, real test of 24
  /// November 2026).
  void _zoom(double live) {
    if (live < 1) {
      _live = live;
      _commit();
    } else {
      setState(() => _live = live);
    }
  }

  void _up(PointerEvent e) {
    _fingers.remove(e.pointer);
    if (_spread0 != null && _fingers.length < 2) {
      _spread0 = null;
      _commit();
    }
    if (_fingers.isEmpty && _pinching) setState(() => _pinching = false);
  }

  void _doubleTap() {
    if (_doc == null || _tapZoom.isAnimating) return;
    _from = _tapAt;
    _to = _tapAt;
    _tapFrom = _scale;
    _tapGoal = _scale > 1.05 ? 1 / _scale : 2.5 / _scale;
    unawaited(_tapZoom.forward(from: 0));
  }

  /// The zoom painted becomes the zoom laid out: the content point under [_from] stays under
  /// [_to], the scroll offsets set before the next layout (set after, one frame showed the
  /// pages elsewhere).
  void _commit() {
    final s0 = _scale;
    var s1 = (s0 * _live).clamp(1.0, PdfScreen.maxScale);
    if (s1 <= 1.001) s1 = 1;
    final k = s1 / s0;
    if (_v.hasClients && _h.hasClients && (k - 1).abs() > 0.0001) {
      final y = (_v.offset + _from.dy - _lead) * k + _lead - _to.dy;
      final x = (_h.offset + _from.dx) * k - _to.dx;
      // Within the pages at the new scale: past their end, the list sprang back.
      _v.position.correctPixels(y.clamp(0.0, math.max(0.0, _contentHeight(s1) - _view.height)));
      _h.position.correctPixels(x.clamp(0.0, math.max(0.0, _view.width * (s1 - 1))));
    }
    for (final t in _tiles.values) {
      _later(t.image);
    }
    _tiles.clear();
    setState(() {
      _scale = s1;
      _live = 1;
    });
    // A pinch going on goes on from here.
    _from = _to;
    if (_spread0 != null) _spread0 = _spreadNow;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      _onScroll();
      _sharpenSoon();
    });
  }

  // ── zoomed in: the part shown drawn sharp ──────────────────────────────────────────────

  void _sharpenSoon() {
    _sharpen?.cancel();
    if (_scale <= 1.05) return;
    _sharpen = Timer(const Duration(milliseconds: 150), () => unawaited(_drawTiles()));
  }

  Future<void> _drawTiles() async {
    final doc = _doc;
    if (doc == null || !mounted || !_v.hasClients || !_h.hasClients || _scale <= 1.05) return;
    final run = ++_tileRun;
    final s = _scale;
    final dpr = MediaQuery.devicePixelRatioOf(context);
    final view = Rect.fromLTWH(_h.offset, _v.offset, _view.width, _view.height);
    final pageW = _pageW(s);
    var top = _lead + 8 * s;
    for (var i = 0; i < doc.pages.length; i++) {
      final h = _heightOf(i, s);
      final page = Rect.fromLTWH(8 * s, top, pageW, h);
      top += h + 8 * s;
      if (page.top > view.bottom) break;
      final seen = page.intersect(view);
      if (seen.width <= 1 || seen.height <= 1) continue;
      final old = _tiles[i];
      final local = seen.shift(-page.topLeft);
      if (old != null && old.scale == s && old.rect.contains(local.topLeft) && old.rect.contains(local.bottomRight - const Offset(0.5, 0.5))) continue;
      final px = Rect.fromLTRB(local.left * dpr, local.top * dpr, local.right * dpr, local.bottom * dpr);
      final image = await doc.render(i, (pageW * dpr).round(), region: px);
      if (!mounted || run != _tileRun || _scale != s) {
        image?.dispose();
        return;
      }
      if (image == null) continue;
      final before = _tiles[i];
      setState(() => _tiles[i] = _Tile(s, Rect.fromLTWH(local.left, local.top, image.width / dpr, image.height / dpr), image));
      if (before != null) _later(before.image);
    }
  }

  /// Disposed once the frame that no longer paints it is done.
  void _later(ui.Image image) => WidgetsBinding.instance.addPostFrameCallback((_) => image.dispose());

  // ── the screen ─────────────────────────────────────────────────────────────────────────

  @override
  Widget build(BuildContext context) {
    final accent = playerAccent(context);
    final dark = ThemeData(
      brightness: Brightness.dark,
      colorScheme: ColorScheme.dark(primary: accent, onPrimary: const Color(0xFF0D0F0E), surface: _sheet),
      bottomSheetTheme: const BottomSheetThemeData(backgroundColor: _sheet),
      dialogTheme: const DialogThemeData(backgroundColor: _sheet),
    );
    return Theme(
      data: dark,
      child: AnnotatedRegion<SystemUiOverlayStyle>(
        value: SystemUiOverlayStyle.light,
        child: Scaffold(
          backgroundColor: _ground,
          body: LayoutBuilder(
            builder: (context, box) {
              final pad = MediaQuery.paddingOf(context);
              _view = box.biggest;
              _lead = pad.top + 60;
              _trail = pad.bottom + 120;
              final doc = _doc;
              final dpr = MediaQuery.devicePixelRatioOf(context);
              if (doc != null && _pages == null) _pages = _Renders(doc, ((_view.width - 16) * dpr).round(), keep: 8);
              return Stack(
                fit: StackFit.expand,
                children: [
                  if (doc != null) KeyedSubtree(key: const ValueKey('layer-pages'), child: _pagesView(doc)),
                  if (doc == null && _failure == null) const Center(child: CircularProgressIndicator()),
                  if (_failure != null) _failed(accent),
                  if (doc != null && !_chrome) _chip(),
                  if (_chrome || _failure != null) _header(context),
                  if (doc != null && _chrome) _bottom(context, doc, accent),
                ],
              );
            },
          ),
        ),
      ),
    );
  }

  Widget _pagesView(PdfDocument doc) {
    final s = _scale;
    final n = doc.pages.length;
    final list = ListView.builder(
      key: const Key('pdf-pages'),
      controller: _v,
      physics: _pinching ? const NeverScrollableScrollPhysics() : null,
      padding: EdgeInsets.only(top: _lead + 8 * s, bottom: _trail + 8 * s),
      itemCount: n,
      itemExtentBuilder: (i, _) => i >= n ? null : _heightOf(i, s) + (i < n - 1 ? 8 * s : 0),
      itemBuilder: (context, i) => Align(
        alignment: Alignment.topCenter,
        child: _PdfPage(
          key: ValueKey('pdf-page-$i'),
          index: i,
          width: _pageW(s),
          height: _heightOf(i, s),
          renders: _pages!,
          tile: _tiles[i]?.scale == s ? _tiles[i] : null,
        ),
      ),
    );
    final scroller = NotificationListener<ScrollEndNotification>(
      onNotification: (_) {
        _sharpenSoon();
        return false;
      },
      child: SingleChildScrollView(
        controller: _h,
        scrollDirection: Axis.horizontal,
        physics: _pinching || s <= 1 ? const NeverScrollableScrollPhysics() : const ClampingScrollPhysics(),
        child: SizedBox(width: _view.width * s, height: _view.height, child: list),
      ),
    );
    // While zooming: painted, the content point under [_from] following the fingers to [_to].
    // Always there, nothing at rest: added for the pinch and taken away after, it made Flutter
    // build the lists anew, back at the top of the first page (real test of 24 November 2026).
    final painted = Transform(
      key: const ValueKey('pdf-zoom'),
      transform: _live == 1
          ? Matrix4.identity()
          : (Matrix4.identity()
              ..translateByDouble(_to.dx, _to.dy, 0, 1)
              ..scaleByDouble(_live, _live, 1, 1)
              ..translateByDouble(-_from.dx, -_from.dy, 0, 1)),
      child: scroller,
    );
    return Listener(
      onPointerDown: _down,
      onPointerMove: _move,
      onPointerUp: _up,
      onPointerCancel: _up,
      child: GestureDetector(
        onTap: () => setState(() => _chrome = !_chrome),
        onDoubleTapDown: (d) => _tapAt = d.localPosition,
        onDoubleTap: _doubleTap,
        child: ClipRect(child: painted),
      ),
    );
  }

  Widget _chip() => Positioned(
    right: 12,
    top: MediaQuery.paddingOf(context).top + 10,
    child: IgnorePointer(
      child: ValueListenableBuilder<int>(
        valueListenable: _page,
        builder: (context, page, _) => Container(
          key: const Key('pdf-page-chip'),
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
          decoration: BoxDecoration(color: Colors.black.withValues(alpha: 0.72), borderRadius: BorderRadius.circular(12)),
          child: Text(
            '${page + 1} / ${_doc!.pages.length}${_scale > 1.05 ? ' · ${(_scale * 100).round()} %' : ''}',
            style: const TextStyle(color: Colors.white, fontSize: 12, fontWeight: FontWeight.w800),
          ),
        ),
      ),
    ),
  );

  Widget _header(BuildContext context) {
    final l = AppLocalizations.of(context);
    final f = widget.file;
    final doc = _doc;
    return Positioned(
      left: 0,
      right: 0,
      top: 0,
      child: ColoredBox(
        color: _bar,
        child: SafeArea(
          bottom: false,
          child: Padding(
            padding: const EdgeInsets.all(4),
            child: Row(
              children: [
                IconButton(key: const Key('player-close'), tooltip: l.closePlayer, icon: const Icon(Icons.arrow_back_rounded), onPressed: () => Navigator.of(context).maybePop()),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text(f.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontWeight: FontWeight.w700, fontSize: 15)),
                      ValueListenableBuilder<int>(
                        valueListenable: _page,
                        builder: (context, page, _) => Text(
                          [
                            if (doc != null) l.pdfPageOf(page + 1, doc.pages.length),
                            formatBytes(context, f.size),
                          ].join(' · '),
                          key: const Key('pdf-header-line'),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(color: _muted, fontSize: 12),
                        ),
                      ),
                    ],
                  ),
                ),
                if (doc != null)
                  IconButton(key: const Key('pdf-menu'), tooltip: l.moreOptions, icon: const Icon(Icons.more_vert_rounded), onPressed: () => unawaited(_menu(context))),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _bottom(BuildContext context, PdfDocument doc, Color accent) {
    final l = AppLocalizations.of(context);
    final state = AppScope.of(context).state;
    final n = doc.pages.length;
    return Positioned(
      left: 0,
      right: 0,
      bottom: 0,
      child: ColoredBox(
        color: _bar,
        child: SafeArea(
          top: false,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(10, 6, 10, 10),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                if (n > 1)
                  ValueListenableBuilder<int>(
                    valueListenable: _page,
                    builder: (context, page, _) => Row(
                      children: [
                        SizedBox(width: 30, child: Text('${page + 1}', style: TextStyle(color: accent, fontSize: 12, fontWeight: FontWeight.w800))),
                        Expanded(
                          child: Slider(
                            key: const Key('pdf-slider'),
                            value: (page + 1).toDouble(),
                            min: 1,
                            max: n.toDouble(),
                            divisions: n - 1,
                            label: '${page + 1}',
                            semanticFormatterCallback: (v) => l.pdfPageOf(v.round(), n),
                            onChanged: (v) => _jumpTo(v.round() - 1),
                          ),
                        ),
                        SizedBox(width: 30, child: Text('$n', textAlign: TextAlign.right, style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w700))),
                      ],
                    ),
                  ),
                Row(
                  children: [
                    Flexible(child: MediaSelectButton(file: widget.file, accent: accent)),
                    const Spacer(),
                    IconButton(key: const Key('pdf-goto'), tooltip: l.goToPage, icon: const Icon(Icons.tag_rounded), onPressed: () => unawaited(_goTo(context))),
                    IconButton(key: const Key('pdf-all'), tooltip: l.allPages, icon: const Icon(Icons.grid_view_rounded), onPressed: () => unawaited(_all(context))),
                    if (state.openWith.supported)
                      IconButton(key: const Key('open-with'), tooltip: l.openWithOther, icon: const Icon(Icons.open_in_new_rounded), onPressed: _openWith),
                  ],
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _failed(Color accent) {
    final l = AppLocalizations.of(context);
    final state = AppScope.of(context).state;
    final locked = _failure == _Failure.protected;
    return Center(
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 28),
        child: Column(
          key: Key(locked ? 'pdf-protected' : 'pdf-failed'),
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(locked ? Icons.lock_outline_rounded : Icons.picture_as_pdf_outlined, size: 40, color: _muted),
            const SizedBox(height: 12),
            Text(locked ? l.pdfProtected : l.pdfCannotOpen, textAlign: TextAlign.center, style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w800)),
            const SizedBox(height: 8),
            Text(
              locked ? l.pdfProtectedDetail : l.cannotPlayDetail(_detail),
              textAlign: TextAlign.center,
              style: const TextStyle(color: _muted, fontSize: 13, height: 1.45),
            ),
            const SizedBox(height: 16),
            if (state.openWith.supported)
              FilledButton(
                key: const Key('open-with'),
                style: FilledButton.styleFrom(backgroundColor: accent, foregroundColor: const Color(0xFF0D0F0E), minimumSize: const Size(0, 48)),
                onPressed: _openWith,
                child: Text(l.openWithOther, style: const TextStyle(fontWeight: FontWeight.w800)),
              ),
            const SizedBox(height: 12),
            MediaSelectButton(file: widget.file, accent: accent),
          ],
        ),
      ),
    );
  }

  void _openWith() => unawaited(AppScope.of(context, listen: false).state.openWith.openFile(widget.file.path, 'application/pdf'));

  Future<void> _goTo(BuildContext context) async {
    final n = _doc!.pages.length;
    final picked = await showModalBottomSheet<int>(
      context: context,
      isScrollControlled: true,
      showDragHandle: true,
      builder: (context) => _GoToSheet(pages: n, at: _page.value + 1),
    );
    if (picked != null && mounted) _jumpTo(picked.clamp(1, n) - 1);
  }

  Future<void> _all(BuildContext context) async {
    final doc = _doc!;
    final picked = await Navigator.of(context).push<int>(
      MaterialPageRoute(builder: (_) => Theme(data: Theme.of(context), child: _AllPages(file: widget.file, doc: doc, current: _page.value))),
    );
    if (picked != null && mounted) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _jumpTo(picked);
      });
    }
  }

  Future<void> _menu(BuildContext context) async {
    final l = AppLocalizations.of(context);
    final state = AppScope.of(context, listen: false).state;
    final doc = _doc!;
    final f = widget.file;
    final chosen = await showModalBottomSheet<String>(
      context: context,
      showDragHandle: true,
      builder: (context) {
        Widget row(String key, IconData icon, String label, [String value = '']) => ListTile(
          key: Key('pdf-menu-$key'),
          leading: Icon(icon),
          title: Text(label, style: const TextStyle(fontWeight: FontWeight.w600)),
          trailing: value.isEmpty ? null : Text(value, style: const TextStyle(color: _muted, fontSize: 13)),
          onTap: () => Navigator.of(context).pop(key),
        );
        return SafeArea(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              row('goto', Icons.tag_rounded, l.goToPage),
              row('all', Icons.grid_view_rounded, l.allPages, '${doc.pages.length}'),
              row('info', Icons.info_outline_rounded, l.fileInfo, '${formatBytes(context, f.size)} · ${l.pageCount(doc.pages.length)}'),
              if (state.openWith.supported) row('open', Icons.open_in_new_rounded, l.openWithOther),
              if (state.openWith.supported) row('share', Icons.share_rounded, l.shareMedia),
            ],
          ),
        );
      },
    );
    if (!mounted || !context.mounted) return;
    switch (chosen) {
      case 'goto':
        await _goTo(context);
      case 'all':
        await _all(context);
      case 'info':
        await _info(context);
      case 'open':
        _openWith();
      case 'share':
        unawaited(state.openWith.shareFile(f.path, 'application/pdf'));
    }
  }

  Future<void> _info(BuildContext context) {
    final l = AppLocalizations.of(context);
    final f = widget.file;
    final loc = MaterialLocalizations.of(context);
    final when = DateTime.fromMillisecondsSinceEpoch(f.modified * 1000);
    final slash = f.path.lastIndexOf('/');
    return showDialog<void>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(l.fileInfo),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(f.name, style: const TextStyle(fontWeight: FontWeight.w700)),
            const SizedBox(height: 6),
            Text([formatBytes(context, f.size), l.pageCount(_doc!.pages.length), if (f.modified > 0) loc.formatShortDate(when)].join(' · ')),
            if (slash > 0) ...[const SizedBox(height: 6), Text('${l.fileLocation} : ${f.path.substring(0, slash)}', style: const TextStyle(color: _muted, fontSize: 13))],
          ],
        ),
        actions: [TextButton(onPressed: () => Navigator.of(context).pop(), child: Text(MaterialLocalizations.of(context).okButtonLabel))],
      ),
    );
  }
}

/// « Aller à la page »: a number, then « Aller ». Its field lives as long as the sheet, which
/// still showed it while closing.
class _GoToSheet extends StatefulWidget {
  const _GoToSheet({required this.pages, required this.at});

  final int pages;
  final int at;

  @override
  State<_GoToSheet> createState() => _GoToSheetState();
}

class _GoToSheetState extends State<_GoToSheet> {
  late final _field = TextEditingController(text: '${widget.at}');

  @override
  void dispose() {
    _field.dispose();
    super.dispose();
  }

  void _go() => Navigator.of(context).pop(int.tryParse(_field.text.trim()));

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    return Padding(
      padding: EdgeInsets.fromLTRB(18, 0, 18, 20 + MediaQuery.viewInsetsOf(context).bottom),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(l.goToPage, style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w800)),
          const SizedBox(height: 12),
          TextField(
            key: const Key('pdf-goto-field'),
            controller: _field,
            autofocus: true,
            keyboardType: TextInputType.number,
            inputFormatters: [FilteringTextInputFormatter.digitsOnly],
            decoration: InputDecoration(labelText: l.pageRange(widget.pages), border: const OutlineInputBorder()),
            onSubmitted: (_) => _go(),
          ),
          const SizedBox(height: 12),
          Row(
            mainAxisAlignment: MainAxisAlignment.end,
            children: [
              TextButton(onPressed: () => Navigator.of(context).pop(), child: Text(l.cancel)),
              const SizedBox(width: 8),
              FilledButton(key: const Key('pdf-goto-go'), onPressed: _go, child: Text(l.goAction)),
            ],
          ),
        ],
      ),
    );
  }
}

/// A part of a page drawn at [scale], at [rect] in the page (logical pixels).
class _Tile {
  _Tile(this.scale, this.rect, this.image);
  final double scale;
  final Rect rect;
  final ui.Image image;
}

/// One page: white until drawn, its image stretched to its size, and zoomed in the sharp part
/// shown over it.
class _PdfPage extends StatefulWidget {
  const _PdfPage({super.key, required this.index, required this.width, required this.height, required this.renders, this.tile});

  final int index;
  final double width, height;
  final _Renders renders;
  final _Tile? tile;

  @override
  State<_PdfPage> createState() => _PdfPageState();
}

class _PdfPageState extends State<_PdfPage> {
  @override
  void initState() {
    super.initState();
    widget.renders.show(widget.index);
  }

  @override
  void dispose() {
    widget.renders.hide(widget.index);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final tile = widget.tile;
    return SizedBox(
      width: widget.width,
      height: widget.height,
      child: DecoratedBox(
        decoration: const BoxDecoration(color: Colors.white, boxShadow: [BoxShadow(color: Color(0x80000000), blurRadius: 3, offset: Offset(0, 1))]),
        child: ListenableBuilder(
          listenable: widget.renders,
          builder: (context, _) {
            final image = widget.renders.of(widget.index);
            return Stack(
              fit: StackFit.expand,
              children: [
                if (image != null) RawImage(image: image, fit: BoxFit.fill, filterQuality: FilterQuality.medium),
                if (tile != null) Positioned.fromRect(rect: tile.rect, child: RawImage(image: tile.image, fit: BoxFit.fill)),
              ],
            );
          },
        ),
      ),
    );
  }
}

/// The pages drawn at one width: those on the screen asked for, the last shown asked first, one
/// at a time (the system draws them one by one anyway); [keep] kept, never one on the screen
/// dropped.
class _Renders extends ChangeNotifier {
  _Renders(this.doc, this.width, {required this.keep});

  final PdfDocument doc;
  final int width;
  final int keep;
  final _images = <int, ui.Image>{}; // oldest first
  final _shown = <int, int>{}; // page -> how many widgets show it
  final _wanted = <int>[];
  bool _busy = false;
  bool _gone = false;

  ui.Image? of(int i) => _images[i];

  void show(int i) {
    _shown[i] = (_shown[i] ?? 0) + 1;
    final had = _images.remove(i);
    if (had != null) {
      _images[i] = had; // the most recent
      return;
    }
    _wanted
      ..remove(i)
      ..add(i);
    unawaited(_pump());
  }

  void hide(int i) {
    final left = (_shown[i] ?? 1) - 1;
    if (left > 0) {
      _shown[i] = left;
      return;
    }
    _shown.remove(i);
    _wanted.remove(i); // scrolled past before drawn: not drawn
  }

  Future<void> _pump() async {
    if (_busy) return;
    _busy = true;
    try {
      while (_wanted.isNotEmpty && !_gone) {
        final i = _wanted.removeLast();
        final image = await doc.render(i, width);
        if (_gone) {
          image?.dispose();
          return;
        }
        if (image == null) continue;
        _images[i] = image;
        _trim();
        notifyListeners();
      }
    } finally {
      _busy = false;
    }
  }

  void _trim() {
    while (_images.length > keep) {
      final old = _images.keys.firstWhere((k) => !_shown.containsKey(k), orElse: () => -1);
      if (old < 0) return;
      final image = _images.remove(old)!;
      WidgetsBinding.instance.addPostFrameCallback((_) => image.dispose());
    }
  }

  @override
  void dispose() {
    _gone = true;
    for (final image in _images.values) {
      image.dispose();
    }
    _images.clear();
    super.dispose();
  }
}

/// All the pages as thumbnails (mockup 28): touching one goes to it.
class _AllPages extends StatefulWidget {
  const _AllPages({required this.file, required this.doc, required this.current});

  final FileEntry file;
  final PdfDocument doc;
  final int current;

  @override
  State<_AllPages> createState() => _AllPagesState();
}

class _AllPagesState extends State<_AllPages> {
  _Renders? _thumbs;
  late final ScrollController _scroll;

  @override
  void initState() {
    super.initState();
    // The current page's row near the top.
    _scroll = ScrollController(initialScrollOffset: math.max(0, (widget.current ~/ 3 - 1) * 200.0));
  }

  @override
  void dispose() {
    _thumbs?.dispose();
    _scroll.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final accent = playerAccent(context);
    final n = widget.doc.pages.length;
    return Scaffold(
      backgroundColor: const Color(0xFF0D0F0E),
      appBar: AppBar(
        backgroundColor: const Color(0xFF0D0F0E),
        leading: IconButton(tooltip: l.backToReading, icon: const Icon(Icons.arrow_back_rounded), onPressed: () => Navigator.of(context).maybePop()),
        titleSpacing: 0,
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(l.allPages, style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w700)),
            Text('${widget.file.name} · ${l.pageCount(n)}', maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(color: _muted, fontSize: 12)),
          ],
        ),
      ),
      body: LayoutBuilder(
        builder: (context, box) {
          final w = (box.maxWidth - 24 - 20) / 3;
          _thumbs ??= _Renders(widget.doc, (w * MediaQuery.devicePixelRatioOf(context)).round(), keep: 36);
          return Semantics(
            label: l.pdfPages,
            child: GridView.builder(
              key: const Key('pdf-all-pages'),
              controller: _scroll,
              padding: const EdgeInsets.fromLTRB(12, 4, 12, 24),
              gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(crossAxisCount: 3, crossAxisSpacing: 10, mainAxisSpacing: 14, childAspectRatio: w / (w * 1.414 + 22)),
              itemCount: n,
              itemBuilder: (context, i) {
                final on = i == widget.current;
                final p = widget.doc.pages[i];
                final h = p.width <= 0 ? w * 1.414 : w * p.height / p.width;
                return Semantics(
                  button: true,
                  selected: on,
                  label: on ? l.pdfCurrentPage(i + 1) : l.pdfPage(i + 1),
                  excludeSemantics: true,
                  child: InkWell(
                    key: ValueKey('pdf-thumb-$i'),
                    onTap: () => Navigator.of(context).pop(i),
                    child: Column(
                      children: [
                        Expanded(
                          child: Center(
                            child: Container(
                              decoration: on ? BoxDecoration(border: Border.all(color: accent, width: 3)) : null,
                              padding: on ? const EdgeInsets.all(2) : EdgeInsets.zero,
                              child: _PdfPage(index: i, width: w - (on ? 10 : 0), height: math.min(h, w * 1.414) - (on ? 10 : 0), renders: _thumbs!),
                            ),
                          ),
                        ),
                        const SizedBox(height: 6),
                        Text('${i + 1}', style: TextStyle(fontSize: 12, fontWeight: on ? FontWeight.w800 : FontWeight.w600, color: on ? accent : _muted)),
                      ],
                    ),
                  ),
                );
              },
            ),
          );
        },
      ),
    );
  }
}
