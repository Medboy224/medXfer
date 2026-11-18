/// The toolbar over the explorer's tabs (plan J8, WP-8.1b): what the tab shows and in which
/// order; in Photos, Videos and Music, search, sort and layout; and « Tout sélectionner ».
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../l10n/app_localizations.dart';
import '../../library/explorer.dart';
import '../../library/selection.dart' show Pick;
import '../../platform/library.dart';
import '../../theme.dart';
import '../../widgets/v4.dart';

/// The kind of media of a tab of the home screen, if it shows media.
MediaKind? kindOfTab(int index) => switch (index) {
  2 => MediaKind.images,
  3 => MediaKind.video,
  4 => MediaKind.audio,
  _ => null,
};

class GalleryToolbar extends StatefulWidget {
  const GalleryToolbar({super.key});

  @override
  State<GalleryToolbar> createState() => _GalleryToolbarState();
}

class _GalleryToolbarState extends State<GalleryToolbar> {
  final _query = TextEditingController();
  Timer? _debounce;
  bool _searching = false;
  int? _tab;

  @override
  void dispose() {
    _debounce?.cancel();
    _query.dispose();
    super.dispose();
  }

  /// A short pause after the last letter, then the platform searches the whole library.
  void _typed(MediaKind kind, String text) {
    _debounce?.cancel();
    _debounce = Timer(const Duration(milliseconds: 350), () {
      final ex = AppScope.of(context, listen: false).state.explorer;
      ex.setOrder(kind, ex.order(kind).copyWith(search: text.trim()));
    });
  }

  void _close(MediaKind kind) {
    _debounce?.cancel();
    _query.clear();
    final ex = AppScope.of(context, listen: false).state.explorer;
    ex.setOrder(kind, ex.order(kind).copyWith(search: ''));
    setState(() => _searching = false);
  }

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final tabs = DefaultTabController.of(context);
    return ListenableBuilder(
      listenable: Listenable.merge([tabs, state.selection, state.explorer]),
      builder: (context, _) {
        final ex = state.explorer;
        final kind = state.library.supported ? kindOfTab(tabs.index) : null;
        // Another tab: its own search, shown if it has one.
        if (_tab != tabs.index) {
          _tab = tabs.index;
          final search = kind == null ? '' : ex.order(kind).search;
          _searching = search.isNotEmpty;
          if (_query.text != search) _query.text = search;
        }
        final picks = state.library.supported ? ex.picksOf(tabs.index) : const <Pick>[];
        final all = state.selection.allOf(picks);
        final selectAll = IconButton(
          key: const Key('select-all'),
          tooltip: l.selectAll,
          onPressed: picks.isEmpty
              ? null
              : () {
                  state.selection.toggleAll(picks);
                  ex.sizeFolders(state.selection);
                },
          icon: Opacity(opacity: picks.isEmpty ? 0.4 : 1, child: PickBox(on: all, size: 22)),
        );
        if (_searching && kind != null) {
          final o = ex.order(kind);
          final list = ex.media(kind);
          return Padding(
            padding: const EdgeInsets.fromLTRB(12, 2, 4, 4),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: TextField(
                        key: const Key('gallery-search'),
                        controller: _query,
                        autofocus: true,
                        textInputAction: TextInputAction.search,
                        style: const TextStyle(fontSize: 14),
                        decoration: InputDecoration(
                          isDense: true,
                          hintText: l.searchMediaHint,
                          prefixIcon: const Icon(Icons.search_rounded, size: 20),
                          contentPadding: const EdgeInsets.symmetric(vertical: 12),
                        ),
                        onChanged: (t) => _typed(kind, t),
                      ),
                    ),
                    IconButton(key: const Key('gallery-search-close'), tooltip: l.closeSearch, icon: const Icon(Icons.close_rounded), onPressed: () => _close(kind)),
                    selectAll,
                  ],
                ),
                if (o.search.isNotEmpty && list.done && list.items.isEmpty)
                  Padding(
                    padding: const EdgeInsets.fromLTRB(4, 4, 4, 0),
                    child: Text(l.noResults(o.search), style: TextStyle(color: c.muted, fontSize: 12)),
                  ),
              ],
            ),
          );
        }
        final String line;
        if (kind == null) {
          line = l.onThisDevice;
        } else {
          final o = ex.order(kind);
          line = '${_sortName(l, kind, o.sort)} · ${_direction(l, o)}';
        }
        return SizedBox(
          height: 44,
          child: Row(
            children: [
              const SizedBox(width: 14),
              Expanded(
                child: Text(line, key: const Key('gallery-line'), maxLines: 1, overflow: TextOverflow.ellipsis, style: TextStyle(color: c.muted, fontWeight: FontWeight.w600, fontSize: 13)),
              ),
              if (kind != null) ...[
                IconButton(
                  key: const Key('gallery-search-open'),
                  tooltip: l.searchMedia,
                  icon: const Icon(Icons.search_rounded, size: 20),
                  onPressed: () => setState(() => _searching = true),
                ),
                IconButton(
                  key: const Key('gallery-sort'),
                  tooltip: l.sortMedia,
                  icon: const Icon(Icons.swap_vert_rounded, size: 20),
                  onPressed: () => unawaited(_sortSheet(context, kind)),
                ),
                if (kind != MediaKind.audio)
                  IconButton(
                    key: const Key('gallery-layout'),
                    tooltip: l.layoutMedia,
                    icon: Icon(ex.asList(kind) ? Icons.view_list_rounded : Icons.grid_view_rounded, size: 20),
                    onPressed: () => unawaited(_layoutSheet(context, kind)),
                  ),
              ],
              selectAll,
              const SizedBox(width: 2),
            ],
          ),
        );
      },
    );
  }
}

String _sortName(AppLocalizations l, MediaKind kind, MediaSort s) => switch (s) {
  MediaSort.date => l.sortByDate,
  MediaSort.name => kind == MediaKind.audio ? l.sortByTitle : l.sortByName,
  MediaSort.size => l.sortBySize,
};

String _direction(AppLocalizations l, MediaOrder o) => switch (o.sort) {
  MediaSort.date => o.descending ? l.orderNewest : l.orderOldest,
  MediaSort.name => o.descending ? l.orderZA : l.orderAZ,
  MediaSort.size => o.descending ? l.orderLargest : l.orderSmallest,
};

Future<void> _sortSheet(BuildContext context, MediaKind kind) => showModalBottomSheet<void>(
  context: context,
  builder: (_) => _SortSheet(kind: kind, explorer: AppScope.of(context, listen: false).state.explorer),
);

Future<void> _layoutSheet(BuildContext context, MediaKind kind) => showModalBottomSheet<void>(
  context: context,
  builder: (_) => _LayoutSheet(kind: kind, explorer: AppScope.of(context, listen: false).state.explorer),
);

/// Sort by date, name or size (one choice: rounds), then in which direction.
class _SortSheet extends StatelessWidget {
  const _SortSheet({required this.kind, required this.explorer});

  final MediaKind kind;
  final Explorer explorer;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    return ListenableBuilder(
      listenable: explorer,
      builder: (context, _) {
        final o = explorer.order(kind);
        // A name reads A → Z first; a date and a size, newest and largest first.
        bool firstOf(MediaSort s) => s != MediaSort.name;
        return SafeArea(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(8, 16, 8, 12),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Padding(padding: const EdgeInsets.fromLTRB(12, 0, 12, 4), child: Text(l.sortMedia, style: titleStyle(16))),
                RadioGroup<MediaSort>(
                  groupValue: o.sort,
                  onChanged: (s) {
                    if (s != null && s != o.sort) explorer.setOrder(kind, o.copyWith(sort: s, descending: firstOf(s)));
                  },
                  child: Column(
                    children: [
                      for (final s in MediaSort.values)
                        RadioListTile<MediaSort>(key: ValueKey('sort-${s.name}'), value: s, title: Text(_sortName(l, kind, s), style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w600))),
                    ],
                  ),
                ),
                Padding(
                  padding: const EdgeInsets.fromLTRB(12, 8, 12, 0),
                  child: SegmentedButton<bool>(
                    showSelectedIcon: false,
                    segments: [
                      ButtonSegment(value: firstOf(o.sort), label: Text(_direction(l, o.copyWith(descending: firstOf(o.sort))), key: const Key('order-first'))),
                      ButtonSegment(value: !firstOf(o.sort), label: Text(_direction(l, o.copyWith(descending: !firstOf(o.sort))), key: const Key('order-second'))),
                    ],
                    selected: {o.descending},
                    onSelectionChanged: (v) => explorer.setOrder(kind, o.copyWith(descending: v.first)),
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

/// A grid of 2 to 5 columns, or a list; and how to pinch.
class _LayoutSheet extends StatelessWidget {
  const _LayoutSheet({required this.kind, required this.explorer});

  final MediaKind kind;
  final Explorer explorer;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return ListenableBuilder(
      listenable: explorer,
      builder: (context, _) {
        final current = explorer.asList(kind) ? 0 : explorer.columns(kind);
        return SafeArea(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(20, 16, 20, 16),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text(l.layoutMedia, style: titleStyle(16)),
                const SizedBox(height: 12),
                SegmentedButton<int>(
                  showSelectedIcon: false,
                  segments: [
                    for (var n = Explorer.minColumns; n <= Explorer.maxColumns; n++)
                      ButtonSegment(value: n, label: Text(l.layoutColumns(n), key: ValueKey('layout-$n'))),
                    ButtonSegment(value: 0, label: Text(l.layoutList, key: const Key('layout-list'))),
                  ],
                  selected: {current},
                  onSelectionChanged: (v) => v.first == 0 ? explorer.setLayout(kind, asList: true) : explorer.setLayout(kind, columns: v.first, asList: false),
                ),
                const SizedBox(height: 12),
                Text(l.pinchHint, style: TextStyle(color: c.muted, fontSize: 12, height: 1.4)),
              ],
            ),
          ),
        );
      },
    );
  }
}
