/// A video (WP-8.2, mockup 21): played where it is, with its commands, « Sélectionner », the
/// next ones; full screen turns the phone for a wide picture. A format the phone does not read is
/// said, with another app to try.
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app.dart';
import '../../l10n/app_localizations.dart';
import '../../media/media_player.dart';
import '../../platform/library.dart';
import '../../theme.dart';
import '../explorer/explorer_tabs.dart' show LibraryImage;
import 'open_media.dart';
import 'player_controls.dart';

class VideoScreen extends StatefulWidget {
  const VideoScreen({super.key, required this.items, required this.index});

  final List<MediaEntry> items;
  final int index;

  @override
  State<VideoScreen> createState() => _VideoScreenState();
}

class _VideoScreenState extends State<VideoScreen> {
  late MediaPlayer _player;
  late int _index = widget.index;
  bool _full = false;
  bool _started = false;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_started) return;
    _started = true;
    _player = AppScope.of(context, listen: false).state.players.create();
    unawaited(_open(_index));
  }

  Future<void> _open(int i) async {
    if (i < 0 || i >= widget.items.length) return;
    setState(() => _index = i);
    await _player.open(widget.items[i]);
    if (mounted && _player.error == null) await _player.play();
  }

  Future<void> _setFull(bool full) async {
    setState(() => _full = full);
    if (full) {
      await SystemChrome.setEnabledSystemUIMode(SystemUiMode.immersiveSticky);
      if (_player.aspect > 1) await SystemChrome.setPreferredOrientations([DeviceOrientation.landscapeLeft, DeviceOrientation.landscapeRight]);
    } else {
      await SystemChrome.setPreferredOrientations(const []);
      await SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge);
    }
  }

  @override
  void dispose() {
    if (_full) {
      unawaited(SystemChrome.setPreferredOrientations(const []));
      unawaited(SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge));
    }
    _player.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final m = widget.items[_index];
    return Theme(
      data: ThemeData.dark(useMaterial3: true).copyWith(colorScheme: ColorScheme.dark(primary: c.accent)),
      child: PopScope(
        canPop: !_full,
        onPopInvokedWithResult: (didPop, _) {
          if (!didPop) unawaited(_setFull(false)); // back leaves the full screen first
        },
        child: Scaffold(
          backgroundColor: Colors.black,
          body: ListenableBuilder(
            listenable: _player,
            builder: (context, _) {
              final p = _player;
              final failed = p.error != null;
              final picture = GestureDetector(
                key: const Key('video-picture'),
                onTap: failed || !p.ready ? null : () => unawaited(p.playing ? p.pause() : p.play()),
                child: ColoredBox(
                  color: Colors.black,
                  child: Stack(
                    alignment: Alignment.center,
                    children: [
                      if (p.ready && !failed) AspectRatio(aspectRatio: p.aspect > 0 ? p.aspect : 16 / 9, child: p.view()),
                      if (!p.ready && !failed) const CircularProgressIndicator(),
                      // Said under the picture: in it, the words did not fit (with a large text above all).
                      if (failed) const Icon(Icons.videocam_off_rounded, size: 48, color: Colors.white54),
                      if (p.ready && !failed && !p.playing)
                        Semantics(
                          label: l.playMedia,
                          child: Container(
                            width: 64,
                            height: 64,
                            decoration: BoxDecoration(color: Colors.black54, shape: BoxShape.circle, border: Border.all(color: Colors.white70, width: 2)),
                            child: const Icon(Icons.play_arrow_rounded, size: 34, color: Colors.white),
                          ),
                        ),
                    ],
                  ),
                ),
              );
              if (_full) {
                return Stack(
                  children: [
                    Positioned.fill(child: picture),
                    Positioned(
                      right: 8,
                      bottom: 8,
                      child: SafeArea(
                        child: IconButton(
                          key: const Key('player-exit-full'),
                          tooltip: l.exitFullScreen,
                          style: IconButton.styleFrom(backgroundColor: Colors.black54),
                          icon: const Icon(Icons.fullscreen_exit_rounded),
                          onPressed: () => unawaited(_setFull(false)),
                        ),
                      ),
                    ),
                  ],
                );
              }
              final next = [for (var i = _index + 1; i < widget.items.length && i <= _index + 3; i++) i];
              return SafeArea(
                child: ListView(
                  padding: const EdgeInsets.only(bottom: 16),
                  children: [
                    PlayerHeader(entry: m, line: '${l.positionOf(_index + 1, widget.items.length)} · ${mediaDetails(context, m)}'),
                    AspectRatio(aspectRatio: 16 / 9, child: picture),
                    if (failed)
                      CannotPlay(
                        accent: c.accent,
                        unsupported: !state.players.supported,
                        onOpenWith: state.openWith.supported ? () => unawaited(state.openWith.open(m)) : null,
                      ),
                    if (!failed) ...[
                      const SizedBox(height: 8),
                      PlayerControls(
                        player: p,
                        accent: c.accent,
                        onPrevious: _index > 0 ? () => unawaited(_open(_index - 1)) : null,
                        onNext: _index < widget.items.length - 1 ? () => unawaited(_open(_index + 1)) : null,
                        onFullScreen: p.ready ? () => unawaited(_setFull(true)) : null,
                      ),
                    ],
                    Padding(padding: const EdgeInsets.fromLTRB(14, 8, 14, 0), child: MediaSelectButton(entry: m, accent: c.accent)),
                    if (next.isNotEmpty) ...[
                      Padding(
                        padding: const EdgeInsets.fromLTRB(16, 18, 16, 6),
                        child: Text(l.upNext, style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w800, color: Colors.white70)),
                      ),
                      for (final i in next)
                        ListTile(
                          key: ValueKey('up-next-${widget.items[i].id}'),
                          onTap: () => unawaited(_open(i)),
                          leading: ClipRRect(
                            borderRadius: BorderRadius.circular(8),
                            child: SizedBox(
                              width: 72,
                              height: 44,
                              child: LibraryImage(
                                cacheKey: 'video:${widget.items[i].id}',
                                load: (px) => state.library.thumbnail(MediaKind.video, widget.items[i].id, px),
                              ),
                            ),
                          ),
                          title: Text(widget.items[i].name, maxLines: 1, overflow: TextOverflow.ellipsis),
                          subtitle: Text(mediaDetails(context, widget.items[i]), maxLines: 1, overflow: TextOverflow.ellipsis),
                        ),
                    ],
                  ],
                ),
              );
            },
          ),
        ),
      ),
    );
  }
}
