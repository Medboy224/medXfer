/// The commands of a video or a song (WP-8.2, mockups 21 and 22): the position, the previous,
/// play or pause, the next, the volume; each named for a screen reader, 44 px at least.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../l10n/app_localizations.dart';
import '../../media/media_player.dart';
import '../../widgets/format.dart';

class PlayerControls extends StatefulWidget {
  const PlayerControls({super.key, required this.player, required this.accent, this.onPrevious, this.onNext, this.onFullScreen});

  final MediaPlayer player;
  final Color accent;
  final VoidCallback? onPrevious, onNext, onFullScreen;

  @override
  State<PlayerControls> createState() => _PlayerControlsState();
}

class _PlayerControlsState extends State<PlayerControls> {
  double _beforeMute = 1;
  double? _dragging; // the position under the finger, in seconds, until it lifts

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final p = widget.player;
    final total = p.duration.inMilliseconds / 1000;
    final at = _dragging ?? (p.position.inMilliseconds / 1000).clamp(0, total > 0 ? total : 0).toDouble();
    final muted = p.volume <= 0;
    String time(double s) => formatDuration(context, s.round());
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Semantics(
          label: l.seekLabel,
          child: Slider(
            key: const Key('player-seek'),
            value: total > 0 ? at.clamp(0, total).toDouble() : 0,
            max: total > 0 ? total : 1,
            activeColor: widget.accent,
            onChanged: total > 0 ? (v) => setState(() => _dragging = v) : null,
            onChangeEnd: (v) {
              setState(() => _dragging = null);
              unawaited(p.seek(Duration(milliseconds: (v * 1000).round())));
            },
          ),
        ),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 24),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text(time(at), style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w700, color: Colors.white70)),
              Text(time(total), style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w700, color: Colors.white70)),
            ],
          ),
        ),
        Row(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            IconButton(key: const Key('player-previous'), tooltip: l.previousItem, iconSize: 30, icon: const Icon(Icons.skip_previous_rounded), onPressed: widget.onPrevious),
            const SizedBox(width: 16),
            IconButton.filled(
              key: const Key('player-play'),
              tooltip: p.playing ? l.pauseMedia : l.playMedia,
              iconSize: 34,
              style: IconButton.styleFrom(backgroundColor: widget.accent, foregroundColor: Colors.black, minimumSize: const Size(68, 68)),
              icon: Icon(p.playing ? Icons.pause_rounded : Icons.play_arrow_rounded),
              onPressed: p.ready ? () => unawaited(p.playing ? p.pause() : p.play()) : null,
            ),
            const SizedBox(width: 16),
            IconButton(key: const Key('player-next'), tooltip: l.nextItem, iconSize: 30, icon: const Icon(Icons.skip_next_rounded), onPressed: widget.onNext),
          ],
        ),
        Row(
          children: [
            IconButton(
              key: const Key('player-mute'),
              tooltip: muted ? l.unmuteSound : l.muteSound,
              icon: Icon(muted ? Icons.volume_off_rounded : Icons.volume_up_rounded),
              onPressed: () {
                if (muted) {
                  unawaited(p.setVolume(_beforeMute > 0 ? _beforeMute : 1));
                } else {
                  _beforeMute = p.volume;
                  unawaited(p.setVolume(0));
                }
              },
            ),
            Expanded(
              child: Semantics(
                label: l.volumeLabel,
                child: Slider(key: const Key('player-volume'), value: p.volume.clamp(0, 1).toDouble(), activeColor: widget.accent, onChanged: (v) => unawaited(p.setVolume(v))),
              ),
            ),
            if (widget.onFullScreen != null)
              IconButton(key: const Key('player-full'), tooltip: l.fullScreen, icon: const Icon(Icons.fullscreen_rounded), onPressed: widget.onFullScreen),
          ],
        ),
      ],
    );
  }
}

/// It does not play here: said, with another app to try.
class CannotPlay extends StatelessWidget {
  const CannotPlay({super.key, required this.onOpenWith, required this.accent, this.unsupported = false});

  final VoidCallback? onOpenWith;
  final Color accent;

  /// No player on this device yet (rather than a format it does not read).
  final bool unsupported;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    return Container(
      key: const Key('cannot-play'),
      margin: const EdgeInsets.all(16),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(color: const Color(0xFF1C1F1D), borderRadius: BorderRadius.circular(14)),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(unsupported ? l.notHereYet : l.cannotPlay, style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w800)),
          if (!unsupported) ...[
            const SizedBox(height: 6),
            Text(l.cannotPlayHint, style: const TextStyle(fontSize: 13, color: Colors.white70)),
          ],
          if (onOpenWith != null) ...[
            const SizedBox(height: 10),
            FilledButton(
              style: FilledButton.styleFrom(backgroundColor: accent, foregroundColor: Colors.black, minimumSize: const Size.fromHeight(44)),
              onPressed: onOpenWith,
              child: Text(l.openWithOther),
            ),
          ],
        ],
      ),
    );
  }
}
