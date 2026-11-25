/// The players on a computer (plan J8, WP-8.2b, mockup 23 of the « Version 4 » canvas): one window
/// for photos, videos and songs. On top, back, the name and where it stands, « Ouvrir avec… » and
/// « Sélectionner »; on the sides, the neighbours; below a video or a song, its controls; at the
/// bottom, the keys. The mouse wheel zooms a photo, the keyboard does the rest.
library;

import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app.dart';
import '../../l10n/app_localizations.dart';
import '../../library/explorer.dart';
import '../../media/media_player.dart';
import '../../platform/library.dart';
import 'open_media.dart';
import 'video_screen.dart' show clock;

const _bar = Color(0xFF111312);
const _line = Color(0xFF2A2E2B);
const _key = Color(0xFF181B19);
const _round = Color(0xFF1C1F1D);
const _muted = Color(0xFFA7AEA6);

class DesktopPlayer extends StatefulWidget {
  const DesktopPlayer({super.key, required this.items, required this.index});

  /// Of one kind: photos, videos or songs.
  final List<MediaEntry> items;
  final int index;

  @override
  State<DesktopPlayer> createState() => _DesktopPlayerState();
}

class _DesktopPlayerState extends State<DesktopPlayer> {
  late int _index = widget.index;
  MediaPlayer? _player;
  final _zoom = TransformationController();
  bool _full = false; // a video over the whole window: the bars gone
  final _focus = FocusNode(debugLabel: 'desktop-player');

  MediaEntry get _m => widget.items[_index];
  bool get _media => _m.kind != MediaKind.images;

  @override
  void initState() {
    super.initState();
    _zoom.addListener(_zoomed);
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_media && _player == null) {
      final state = AppScope.of(context, listen: false).state;
      _player = state.players.create()..addListener(_changed);
      unawaited(_load());
    }
  }

  Future<void> _load() async {
    final p = _player;
    if (p == null) return;
    await p.open(_m);
    if (mounted && p.ready) await p.play();
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  void _zoomed() => setState(() {});

  @override
  void dispose() {
    _zoom
      ..removeListener(_zoomed)
      ..dispose();
    _player
      ?..removeListener(_changed)
      ..dispose();
    _focus.dispose();
    super.dispose();
  }

  void _go(int d) {
    final i = _index + d;
    if (i < 0 || i >= widget.items.length) return;
    setState(() {
      _index = i;
      _zoom.value = Matrix4.identity();
    });
    if (_media) unawaited(_load());
  }

  Future<void> _toggle() async {
    final p = _player;
    if (p == null || !p.ready) return;
    p.playing ? await p.pause() : await p.play();
  }

  Future<void> _seekBy(Duration d) async {
    final p = _player;
    if (p == null || !p.ready) return;
    var to = p.position + d;
    if (to < Duration.zero) to = Duration.zero;
    if (to > p.duration) to = p.duration;
    await p.seek(to);
  }

  Future<void> _volumeBy(double d) async {
    final p = _player;
    if (p == null) return;
    await p.setVolume((p.volume + d).clamp(0.0, 1.0));
  }

  void _close() {
    if (_full) {
      setState(() => _full = false);
    } else {
      unawaited(Navigator.of(context).maybePop());
    }
  }

  void _toggleSelected() {
    final sel = AppScope.of(context, listen: false).state.selection;
    sel.toggle(Explorer.pickMedia(_m)); // as « Sélectionner » does
  }

  /// The keys of mockup 23: a photo's, a video's or a song's.
  KeyEventResult _onKey(FocusNode _, KeyEvent e) {
    if (e is! KeyDownEvent && e is! KeyRepeatEvent) return KeyEventResult.ignored;
    final k = e.logicalKey;
    final ctrl = HardwareKeyboard.instance.isControlPressed;
    if (k == LogicalKeyboardKey.escape) {
      _close();
      return KeyEventResult.handled;
    }
    if (!_media) {
      if (k == LogicalKeyboardKey.arrowLeft) _go(-1);
      if (k == LogicalKeyboardKey.arrowRight) _go(1);
      if (k == LogicalKeyboardKey.digit0 || k == LogicalKeyboardKey.numpad0) _zoom.value = Matrix4.identity();
      if (k == LogicalKeyboardKey.space) _toggleSelected();
      return KeyEventResult.handled;
    }
    if (k == LogicalKeyboardKey.space) unawaited(_toggle());
    if (k == LogicalKeyboardKey.arrowLeft) ctrl ? _go(-1) : unawaited(_seekBy(const Duration(seconds: -5)));
    if (k == LogicalKeyboardKey.arrowRight) ctrl ? _go(1) : unawaited(_seekBy(const Duration(seconds: 5)));
    if (k == LogicalKeyboardKey.arrowUp) unawaited(_volumeBy(0.1));
    if (k == LogicalKeyboardKey.arrowDown) unawaited(_volumeBy(-0.1));
    if (k == LogicalKeyboardKey.keyF && _m.kind == MediaKind.video) setState(() => _full = !_full);
    return KeyEventResult.handled;
  }

  @override
  Widget build(BuildContext context) {
    final accent = playerAccent(context);
    final l = AppLocalizations.of(context);
    return Theme(
      data: ThemeData(brightness: Brightness.dark, colorScheme: ColorScheme.dark(primary: accent, onPrimary: const Color(0xFF0D0F0E))),
      child: Focus(
        focusNode: _focus,
        autofocus: true,
        onKeyEvent: _onKey,
        child: Scaffold(
          backgroundColor: Colors.black,
          body: Column(
            children: [
              if (!_full) _header(context, accent),
              Expanded(
                child: Padding(
                  padding: _full ? EdgeInsets.zero : const EdgeInsets.symmetric(horizontal: 16, vertical: 20),
                  child: Row(
                    children: [
                      if (!_full) _side(Icons.chevron_left_rounded, l.previousItem, _index > 0 ? () => _go(-1) : null, const Key('desktop-previous')),
                      Expanded(child: Center(child: _content(context, accent))),
                      if (!_full) _side(Icons.chevron_right_rounded, l.nextItem, _index < widget.items.length - 1 ? () => _go(1) : null, const Key('desktop-next')),
                    ],
                  ),
                ),
              ),
              if (_media && !_full) _controls(context, accent),
              if (!_full) _keys(context),
            ],
          ),
        ),
      ),
    );
  }

  Widget _header(BuildContext context, Color accent) {
    final l = AppLocalizations.of(context);
    final state = AppScope.of(context).state;
    final m = _m;
    return DecoratedBox(
      decoration: const BoxDecoration(color: _bar, border: Border(bottom: BorderSide(color: _line))),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
        child: Row(
          children: [
            TextButton.icon(
              key: const Key('player-close'),
              onPressed: () => Navigator.of(context).maybePop(),
              icon: const Icon(Icons.chevron_left_rounded),
              label: Text(l.backLabel, style: const TextStyle(fontWeight: FontWeight.w700)),
              style: TextButton.styleFrom(foregroundColor: Colors.white, minimumSize: const Size(0, 40)),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(m.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w800)),
                  Text(
                    [l.positionOf(_index + 1, widget.items.length), mediaDetails(context, m)].where((s) => s.isNotEmpty).join(' · '),
                    key: const Key('desktop-where'),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(fontSize: 12, color: _muted),
                  ),
                ],
              ),
            ),
            if (state.openWith.supported) ...[
              OutlinedButton(
                key: const Key('open-with'),
                onPressed: () => unawaited(state.openWith.openFile(m.path, mimeOf(m))),
                style: OutlinedButton.styleFrom(
                  foregroundColor: Colors.white,
                  minimumSize: const Size(0, 40),
                  side: const BorderSide(color: Color(0xFF343936)),
                ),
                child: Text(l.openWithShort, style: const TextStyle(fontWeight: FontWeight.w700)),
              ),
              const SizedBox(width: 8),
            ],
            SizedBox(width: 220, child: MediaSelectButton(entry: m, accent: accent)),
          ],
        ),
      ),
    );
  }

  Widget _side(IconData icon, String label, VoidCallback? onTap, Key key) => Padding(
    padding: const EdgeInsets.symmetric(horizontal: 4),
    child: IconButton(
      key: key,
      tooltip: label,
      onPressed: onTap,
      style: IconButton.styleFrom(backgroundColor: _round, fixedSize: const Size(48, 48), foregroundColor: Colors.white, disabledForegroundColor: Colors.white24),
      icon: Icon(icon, size: 26),
    ),
  );

  Widget _content(BuildContext context, Color accent) {
    final l = AppLocalizations.of(context);
    final m = _m;
    switch (m.kind) {
      case MediaKind.images:
        final scale = _zoom.value.getMaxScaleOnAxis();
        return Stack(
          fit: StackFit.expand,
          children: [
            // The mouse wheel zooms, a drag moves, around the pointer.
            InteractiveViewer(
              key: ValueKey('desktop-photo-${m.path}'),
              transformationController: _zoom,
              minScale: 1,
              maxScale: 8,
              child: Image.file(
                File(m.path),
                fit: BoxFit.contain,
                filterQuality: FilterQuality.medium,
                semanticLabel: m.name,
                errorBuilder: (context, error, stack) => Center(
                  child: Text(l.cannotShowImage, key: const Key('desktop-photo-error'), style: const TextStyle(color: _muted)),
                ),
              ),
            ),
            Positioned(
              left: 12,
              bottom: 12,
              child: IgnorePointer(
                child: DecoratedBox(
                  decoration: BoxDecoration(color: Colors.black.withValues(alpha: 0.65), borderRadius: BorderRadius.circular(10)),
                  child: Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                    child: Text(
                      l.zoomHint(scale <= 1.01 ? l.fitWindow : '${(scale * 100).round()} %'),
                      key: const Key('desktop-zoom'),
                      style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w700),
                    ),
                  ),
                ),
              ),
            ),
          ],
        );
      case MediaKind.video:
        final p = _player;
        if (p == null) return const SizedBox.shrink();
        if (p.error != null) return _failed(context, p.error!);
        return GestureDetector(
          onTap: () => unawaited(_toggle()),
          onDoubleTap: () => setState(() => _full = !_full),
          child: Stack(
            alignment: Alignment.center,
            children: [
              AspectRatio(aspectRatio: p.aspect > 0 ? p.aspect : 16 / 9, child: p.ready ? p.view() : const ColoredBox(color: Color(0xFF111111))),
              if (p.ready && !p.playing)
                IgnorePointer(
                  child: Container(
                    width: 72,
                    height: 72,
                    decoration: BoxDecoration(
                      color: Colors.black.withValues(alpha: 0.6),
                      shape: BoxShape.circle,
                      border: Border.all(color: Colors.white.withValues(alpha: 0.85), width: 2),
                    ),
                    child: const Icon(Icons.play_arrow_rounded, size: 40, color: Colors.white),
                  ),
                ),
            ],
          ),
        );
      case MediaKind.audio:
        final p = _player;
        if (p != null && p.error != null) return _failed(context, p.error!);
        return Wrap(
          alignment: WrapAlignment.center,
          crossAxisAlignment: WrapCrossAlignment.center,
          spacing: 32,
          runSpacing: 24,
          children: [
            Container(
              width: 280,
              height: 280,
              decoration: BoxDecoration(color: _key, borderRadius: BorderRadius.circular(24), border: Border.all(color: const Color(0xFF343936))),
              child: Icon(Icons.music_note_rounded, size: 96, color: accent),
            ),
            ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 360),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(m.name, style: const TextStyle(fontSize: 28, fontWeight: FontWeight.w900)),
                  const SizedBox(height: 6),
                  Text(mediaDetails(context, m), style: const TextStyle(fontSize: 15, color: _muted)),
                ],
              ),
            ),
          ],
        );
    }
  }

  Widget _failed(BuildContext context, String detail) {
    final l = AppLocalizations.of(context);
    final state = AppScope.of(context).state;
    return Column(
      key: const Key('cannot-play'),
      mainAxisSize: MainAxisSize.min,
      children: [
        const Icon(Icons.block_rounded, size: 40, color: _muted),
        const SizedBox(height: 12),
        Text(l.cannotPlayComputer, style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w800)),
        const SizedBox(height: 6),
        Text(l.cannotPlayDetail(detail), textAlign: TextAlign.center, style: const TextStyle(color: _muted, fontSize: 13)),
        const SizedBox(height: 16),
        if (state.openWith.supported)
          FilledButton(onPressed: () => unawaited(state.openWith.openFile(_m.path, mimeOf(_m))), child: Text(l.openWithOther)),
      ],
    );
  }

  Widget _controls(BuildContext context, Color accent) {
    final l = AppLocalizations.of(context);
    final p = _player;
    if (p == null) return const SizedBox.shrink();
    final total = p.duration.inMilliseconds;
    return Padding(
      padding: const EdgeInsets.fromLTRB(24, 0, 24, 12),
      child: Row(
        children: [
          IconButton.filled(
            key: const Key('desktop-play'),
            tooltip: p.playing ? l.pauseMedia : l.playMedia,
            onPressed: p.ready ? () => unawaited(_toggle()) : null,
            style: IconButton.styleFrom(backgroundColor: accent, foregroundColor: const Color(0xFF0D0F0E), fixedSize: const Size(52, 52)),
            icon: Icon(p.playing ? Icons.pause_rounded : Icons.play_arrow_rounded, size: 26),
          ),
          const SizedBox(width: 12),
          Text(clock(p.position), style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w700, color: _muted)),
          Expanded(
            child: Slider(
              key: const Key('desktop-seek'),
              value: total <= 0 ? 0 : p.position.inMilliseconds.clamp(0, total).toDouble(),
              max: total <= 0 ? 1 : total.toDouble(),
              semanticFormatterCallback: (v) => '${l.seekLabel} ${clock(Duration(milliseconds: v.round()))}',
              onChanged: total <= 0 ? null : (v) => unawaited(p.seek(Duration(milliseconds: v.round()))),
            ),
          ),
          Text(clock(p.duration), style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w700, color: _muted)),
          const SizedBox(width: 12),
          const Icon(Icons.volume_up_rounded, size: 18, color: _muted),
          SizedBox(
            width: 140,
            child: Slider(
              key: const Key('desktop-volume'),
              value: p.volume.clamp(0.0, 1.0),
              semanticFormatterCallback: (v) => '${l.volumeLabel} ${(v * 100).round()} %',
              onChanged: (v) => unawaited(p.setVolume(v)),
            ),
          ),
          if (_m.kind == MediaKind.video)
            IconButton(
              key: const Key('desktop-full'),
              tooltip: '${l.fullScreen} (F)',
              onPressed: () => setState(() => _full = true),
              icon: const Icon(Icons.open_in_full_rounded, size: 20),
            ),
        ],
      ),
    );
  }

  Widget _keys(BuildContext context) {
    final l = AppLocalizations.of(context);
    final keys = _media
        ? [
            (l.keySpace, l.keyPlayPause),
            ('← →', l.keySeek5),
            ('Ctrl ← →', l.keyNeighbour),
            ('↑ ↓', l.keyVolume),
            if (_m.kind == MediaKind.video) ('F', l.keyFullScreen),
            (l.keyEsc, l.keyClose),
          ]
        : [
            ('← →', l.keyNeighbourPhoto),
            (l.keyWheel, l.keyZoom),
            ('0', l.keyFit),
            (l.keyEsc, l.keyClose),
            (l.keySpace, l.keySelect),
          ];
    return DecoratedBox(
      decoration: const BoxDecoration(border: Border(top: BorderSide(color: _line))),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(24, 10, 24, 16),
        child: ExcludeSemantics(
          child: Wrap(
            spacing: 16,
            runSpacing: 8,
            children: [
              for (final (key, what) in keys)
                Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                      decoration: BoxDecoration(color: _key, borderRadius: BorderRadius.circular(6), border: Border.all(color: const Color(0xFF343936))),
                      child: Text(key, style: const TextStyle(fontSize: 11, fontWeight: FontWeight.w700)),
                    ),
                    const SizedBox(width: 6),
                    Text(what, style: const TextStyle(fontSize: 12, color: _muted)),
                  ],
                ),
            ],
          ),
        ),
      ),
    );
  }
}
