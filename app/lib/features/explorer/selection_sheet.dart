// What is picked, across every tab, in a sheet from the basket: seen, and taken out one by one
// or all at once (plan J8, WP-8.1b; asked on 22 November 2026: a pick made in another tab could
// not be seen without going back to it).
import 'package:flutter/material.dart';

import '../../app.dart';
import '../../l10n/app_localizations.dart';
import '../../library/selection.dart';
import '../../theme.dart';
import '../../widgets/format.dart';

/// Opens the sheet of what is picked; it closes by itself once nothing is.
Future<void> showSelectionSheet(BuildContext context) => showModalBottomSheet<void>(
  context: context,
  isScrollControlled: true,
  showDragHandle: true,
  useSafeArea: true,
  builder: (_) => const SelectionSheet(),
);

/// The groups of the sheet, in the tabs' order.
enum _Group { apps, photos, videos, music, files }

_Group _groupOf(Pick p) {
  final thumb = p.thumb ?? '';
  if (p.kind == PickKind.app) return _Group.apps;
  if (thumb.startsWith('images:')) return _Group.photos;
  if (thumb.startsWith('video:')) return _Group.videos;
  if (thumb.startsWith('audio:')) return _Group.music;
  return _Group.files;
}

class SelectionSheet extends StatelessWidget {
  const SelectionSheet({super.key});

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final sel = state.selection;
    return ListenableBuilder(
      listenable: sel,
      builder: (context, _) {
        if (sel.isEmpty) {
          // Nothing left: the sheet goes, once this frame is over.
          WidgetsBinding.instance.addPostFrameCallback((_) {
            if (context.mounted && Navigator.of(context).canPop()) Navigator.of(context).pop();
          });
          return const SizedBox(height: 120);
        }
        final groups = <_Group, List<Pick>>{};
        for (final p in sel.items) {
          groups.putIfAbsent(_groupOf(p), () => []).add(p);
        }
        String title(_Group g) => switch (g) {
          _Group.apps => l.tabApps,
          _Group.photos => l.tabPhotos,
          _Group.videos => l.tabVideos,
          _Group.music => l.tabMusic,
          _Group.files => l.tabFiles,
        };
        return DraggableScrollableSheet(
          expand: false,
          initialChildSize: 0.6,
          minChildSize: 0.3,
          maxChildSize: 0.95,
          builder: (context, scroll) => Column(
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(20, 0, 8, 8),
                child: Row(
                  children: [
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(l.selectionTitle, style: titleStyle(18)),
                          Text(
                            l.basketLine(sel.count, formatBytes(context, sel.bytes)),
                            key: const Key('selection-sheet-line'),
                            style: TextStyle(color: c.muted, fontSize: 13, fontFeatures: tabular),
                          ),
                        ],
                      ),
                    ),
                    TextButton.icon(
                      key: const Key('selection-clear'),
                      onPressed: sel.clear,
                      icon: const Icon(Icons.remove_done_rounded, size: 18),
                      label: Text(l.clearSelection),
                    ),
                  ],
                ),
              ),
              Expanded(
                child: ListView(
                  controller: scroll,
                  padding: const EdgeInsets.only(bottom: 24),
                  children: [
                    for (final g in _Group.values)
                      if (groups[g] case final picks?) ...[
                        Padding(
                          padding: const EdgeInsets.fromLTRB(20, 12, 20, 4),
                          child: Text('${title(g)} · ${picks.length}', style: TextStyle(color: c.muted, fontSize: 13, fontWeight: FontWeight.w700)),
                        ),
                        for (final p in picks) _PickRow(pick: p),
                      ],
                  ],
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

/// One picked item: its thumbnail when one was seen, its name and size, and the way out.
class _PickRow extends StatelessWidget {
  const _PickRow({required this.pick});

  final Pick pick;

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final bytes = pick.thumb == null ? null : state.explorer.cached(pick.thumb!);
    final icon = switch (pick.kind) {
      PickKind.app => Icons.android_rounded,
      PickKind.folder => Icons.folder_rounded,
      PickKind.file => Icons.insert_drive_file_rounded,
      PickKind.media => Icons.image_outlined,
    };
    return ListTile(
      key: ValueKey('selection-${pick.path}'),
      dense: true,
      leading: ClipRRect(
        borderRadius: BorderRadius.circular(6),
        child: SizedBox.square(
          dimension: 44,
          child: bytes == null
              ? ColoredBox(color: c.chip, child: Icon(icon, color: c.muted))
              : Image.memory(bytes, fit: pick.kind == PickKind.app ? BoxFit.contain : BoxFit.cover, cacheWidth: 132, gaplessPlayback: true),
        ),
      ),
      title: Text(pick.name), // in full, on as many lines as it needs
      subtitle: pick.kind == PickKind.folder ? null : Text(formatBytes(context, pick.size), style: const TextStyle(fontFeatures: tabular)),
      trailing: IconButton(
        key: ValueKey('selection-remove-${pick.path}'),
        tooltip: l.removeFromSelection,
        icon: const Icon(Icons.close_rounded),
        onPressed: () => state.selection.toggle(pick),
      ),
    );
  }
}
