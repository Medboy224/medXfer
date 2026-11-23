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

/// Opens the player of [items] at [index]: images, videos or music, by its kind.
Future<void> openMedia(BuildContext context, List<MediaEntry> items, int index) => Navigator.of(context).push(
  MaterialPageRoute<void>(
    builder: (_) => switch (items[index].kind) {
      MediaKind.images => ImageViewer(items: items, index: index),
      MediaKind.video => VideoScreen(items: items, index: index),
      MediaKind.audio => MusicScreen(items: items, index: index),
    },
  ),
);

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
