/// The players (plan J8, WP-8.2, mockups 19 to 22 of the « Version 4 » canvas, agreed on 23
/// November 2026): what is about to be sent seen in full, without leaving medXfer. Opened by the
/// ⤢ (▶ for a song) of a tile; touching the tile elsewhere still picks it.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../l10n/app_localizations.dart';
import '../../library/explorer.dart';
import '../../platform/library.dart';
import '../../widgets/format.dart';
import '../../widgets/v4.dart';
import 'image_viewer.dart';
import 'music_screen.dart';
import 'video_screen.dart';

/// Opens the player of [items] at [index]: images, videos or music, by its kind. [onLeave]: the
/// photo shown as the viewer of photos closes.
Future<void> openMedia(BuildContext context, List<MediaEntry> items, int index, {ValueChanged<MediaEntry>? onLeave}) => Navigator.of(context).push(
  items[index].kind == MediaKind.images
      ? _PhotoRoute(ImageViewer(items: items, index: index, onLeave: onLeave))
      : MaterialPageRoute<void>(
          builder: (_) => switch (items[index].kind) {
            MediaKind.video => VideoScreen(items: items, index: index),
            _ => MusicScreen(items: items, index: index),
          },
        ),
);

/// The tag of a photo flying between its tile and the viewer.
Object mediaHeroTag(MediaEntry m) => 'media-hero-${m.kind.name}-${m.id}';

/// How a photo flies between its tile and the viewer: moving from the first frame, slowing into
/// place. Flutter flies its heroes on [Curves.fastOutSlowIn], which starts slowly: the photo
/// stayed by its tile for 3 frames on the black as it opened, and still over the grid back
/// around it as it closed (filmed at 60 frames a second, 23 November 2026).
const mediaFlightCurve = Cubic(0.22, 0.25, 0, 1);

/// The rect of a photo in flight ([Hero.createRectTween]), on [mediaFlightCurve].
RectTween mediaRectTween(Rect? begin, Rect? end) => _MediaRectTween(begin: begin, end: end);

class _MediaRectTween extends MaterialRectArcTween {
  _MediaRectTween({super.begin, super.end});

  // Heroes give their progress through fastOutSlowIn: undone, then on the flight's own curve.
  @override
  Rect lerp(double t) => super.lerp(mediaFlightCurve.transform(_beforeSlowIn(t)));
}

/// When [Curves.fastOutSlowIn] reaches [t]: found by halving, as it only rises.
double _beforeSlowIn(double t) {
  if (t <= 0 || t >= 1) return t.clamp(0.0, 1.0);
  var lo = 0.0, hi = 1.0;
  for (var i = 0; i < 20; i++) {
    final mid = (lo + hi) / 2;
    if (Curves.fastOutSlowIn.transform(mid) < t) {
      lo = mid;
    } else {
      hi = mid;
    }
  }
  return (lo + hi) / 2;
}

/// The viewer of photos opens as a phone's gallery does (filmed on 23 November 2026): black, the
/// photo growing from its tile to the screen in 0.22 s, the controls coming in after; closing,
/// the grid back at once behind the photo, which shrinks into its tile.
/// The black comes over the first 80 ms: at once, it flashed over the grid before the photo had
/// grown, the whole screen black for a dark one (filmed at 60 frames a second, 23 November 2026).
class _PhotoRoute extends PageRouteBuilder<void> {
  _PhotoRoute(Widget viewer)
    : super(
        opaque: false,
        transitionDuration: const Duration(milliseconds: 220),
        reverseTransitionDuration: const Duration(milliseconds: 200),
        pageBuilder: (_, _, _) => viewer,
        transitionsBuilder: (context, animation, _, child) => AnimatedBuilder(
          animation: animation,
          child: child,
          builder: (context, child) {
            final status = animation.status;
            final leaving = status == AnimationStatus.reverse || status == AnimationStatus.dismissed;
            final black = leaving ? 0.0 : Curves.easeOut.transform((animation.value / blackIn).clamp(0.0, 1.0));
            return Stack(
              fit: StackFit.expand,
              children: [
                if (black > 0) ColoredBox(key: const Key('photo-backdrop'), color: Colors.black.withValues(alpha: black)),
                child!,
              ],
            );
          },
        ),
      );

  /// The share of the opening the black takes to come in: 80 of its 220 ms.
  static const blackIn = 80 / 220;
}

/// What a media is: its size, its picture's size, its date, its length.
String mediaDetails(BuildContext context, MediaEntry m) {
  final when = DateTime.fromMillisecondsSinceEpoch(m.modified * 1000);
  final loc = MaterialLocalizations.of(context);
  return [
    formatBytes(context, m.size),
    if (m.width > 0 && m.height > 0) '${m.width} × ${m.height}',
    if (m.durationMs > 0) formatDuration(context, m.durationMs ~/ 1000),
    if (m.modified > 0) '${loc.formatShortDate(when)} ${loc.formatTimeOfDay(TimeOfDay.fromDateTime(when))}',
  ].join(' · ');
}

/// « Sélectionner »: the same selection as the grid's, the basket said once picked.
class MediaSelectButton extends StatelessWidget {
  const MediaSelectButton({super.key, required this.entry, this.accent});

  final MediaEntry entry;

  /// Over a dark picture, the accent of the theme.
  final Color? accent;

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final sel = state.selection;
    final pick = Explorer.pickMedia(entry);
    return ListenableBuilder(
      listenable: sel,
      builder: (context, _) {
        final on = sel.contains(pick.path);
        final edge = accent ?? Theme.of(context).colorScheme.primary;
        return Semantics(
          button: true,
          toggled: on,
          child: Material(
            color: on ? edge.withValues(alpha: 0.15) : Colors.transparent,
            shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14), side: BorderSide(color: on ? edge : Colors.white38, width: 2)),
            child: InkWell(
              key: const Key('media-select'),
              borderRadius: BorderRadius.circular(14),
              onTap: () => sel.toggle(pick),
              child: ConstrainedBox(
                constraints: const BoxConstraints(minHeight: 48),
                child: Row(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    PickBox(on: on, onImage: true, size: 22),
                    const SizedBox(width: 10),
                    Flexible(
                      child: Text(
                        on ? l.selectedWith(l.basketLine(sel.count, formatBytes(context, sel.bytes))) : l.selectThis,
                        style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 14),
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
        );
      },
    );
  }
}

/// A player's top line: close, the name and where it stands, and another app.
class PlayerHeader extends StatelessWidget {
  const PlayerHeader({super.key, required this.entry, required this.line, this.close = Icons.arrow_back_rounded});

  final MediaEntry entry;
  final String line;
  final IconData close;

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(4, 4, 4, 4),
      child: Row(
        children: [
          IconButton(key: const Key('player-close'), tooltip: l.closePlayer, icon: Icon(close), onPressed: () => Navigator.of(context).maybePop()),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(entry.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontWeight: FontWeight.w700, fontSize: 14)),
                Text(line, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(color: Colors.white70, fontSize: 12)),
              ],
            ),
          ),
          if (state.openWith.supported)
            IconButton(
              key: const Key('open-with'),
              tooltip: l.openWithOther,
              icon: const Icon(Icons.open_in_new_rounded),
              onPressed: () => unawaited(state.openWith.open(entry)),
            ),
        ],
      ),
    );
  }
}
