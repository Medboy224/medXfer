/// The tabs of the explorer (WP-B.9, §1.2 of the J6b plan): Apps and Photos in grids, Videos,
/// Music, Files. A tap picks or unpicks; the basket of the home screen shows the selection.
/// Lists are lazy and the images decoded at the size shown (UI-1).
library;

import 'dart:async';

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
                          dimension: 56,
                          child: LibraryImage(cacheKey: 'app:${a.package}', fit: BoxFit.contain, load: (px) => state.library.appIcon(a.package, px)),
                        ),
                        Positioned(right: -8, top: -8, child: PickBox(on: on, size: 24)),
                      ],
                    ),
                    const SizedBox(height: 6),
                    Text(
                      a.label,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600),
                    ),
                    Text(formatBytes(context, a.size), style: TextStyle(color: context.colors.muted, fontSize: 12)),
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

class _MediaGridState extends State<_MediaGrid> with AutomaticKeepAliveClientMixin {
  String? _album;

  @override
  bool get wantKeepAlive => true;

  @override
  void initState() {
    super.initState();
    final explorer = AppScope.of(context, listen: false).state.explorer;
    if (explorer.media(widget.kind).items.isEmpty) unawaited(explorer.more(widget.kind));
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
        final images = widget.kind == MediaKind.images;
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
                child: CustomScrollView(
                  key: ValueKey('media-${widget.kind.name}'),
                  slivers: [
                    if (images)
                      for (final day in _days(items)) ...[
                        SliverToBoxAdapter(child: _DayHeader(day: day.$1, items: day.$2)),
                        SliverGrid(
                          gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(crossAxisCount: 3, mainAxisSpacing: 3, crossAxisSpacing: 3),
                          delegate: SliverChildBuilderDelegate((context, i) => _MediaTile(kind: widget.kind, entry: day.$2[i]), childCount: day.$2.length),
                        ),
                      ]
                    else
                      SliverPadding(
                        padding: const EdgeInsets.fromLTRB(12, 0, 12, 0),
                        sliver: SliverGrid(
                          gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(crossAxisCount: 2, mainAxisSpacing: 16, crossAxisSpacing: 10, mainAxisExtent: 156),
                          delegate: SliverChildBuilderDelegate((context, i) => _VideoTile(entry: items[i]), childCount: items.length),
                        ),
                      ),
                    SliverToBoxAdapter(child: SizedBox(height: bottomRoom(context))),
                  ],
                ),
              ),
            ),
            if (list.loading) LinearProgressIndicator(minHeight: 2, color: c.muted),
          ],
        );
      },
    );
  }

  /// The photos by day, newest first: what « Tout ce jour » picks.
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
          Expanded(child: Text(name, style: titleStyle(15))),
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
  const _MediaTile({required this.kind, required this.entry});

  final MediaKind kind;
  final MediaEntry entry;

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
            if (on) DecoratedBox(decoration: BoxDecoration(border: Border.all(color: c.strong, width: 4))),
            Positioned(right: 8, top: 8, child: PickBox(on: on, onImage: true)),
          ],
        ),
      ),
    );
  }
}

/// A video: its picture with the duration, its name and size under it.
class _VideoTile extends StatelessWidget {
  const _VideoTile({required this.entry});

  final MediaEntry entry;

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
      child: GestureDetector(
        key: ValueKey('media-${m.id}'),
        behavior: HitTestBehavior.opaque,
        onTap: () => state.selection.toggle(pick),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              height: 108,
              child: ClipRRect(
                borderRadius: BorderRadius.circular(controlRadius),
                child: Stack(
                  fit: StackFit.expand,
                  children: [
                    LibraryImage(cacheKey: 'video:${m.id}', load: (px) => state.library.thumbnail(MediaKind.video, m.id, px)),
                    if (on)
                      DecoratedBox(
                        decoration: BoxDecoration(border: Border.all(color: c.strong, width: 4), borderRadius: BorderRadius.circular(controlRadius)),
                      ),
                    Positioned(
                      left: 8,
                      bottom: 8,
                      child: Container(
                        padding: const EdgeInsets.fromLTRB(6, 3, 8, 3),
                        decoration: BoxDecoration(color: Colors.black.withValues(alpha: 0.72), borderRadius: BorderRadius.circular(8)),
                        child: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            const Icon(Icons.play_arrow_rounded, size: 14, color: Colors.white),
                            const SizedBox(width: 2),
                            Text(
                              formatDuration(context, m.durationMs ~/ 1000),
                              style: const TextStyle(color: Colors.white, fontSize: 13, fontWeight: FontWeight.w700, fontFeatures: tabular),
                            ),
                          ],
                        ),
                      ),
                    ),
                    Positioned(right: 8, top: 8, child: PickBox(on: on, onImage: true)),
                  ],
                ),
              ),
            ),
            const SizedBox(height: 6),
            Text(m.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w700)),
            Text(formatBytes(context, m.size), style: TextStyle(fontSize: 13, color: c.muted, fontFeatures: tabular)),
          ],
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
            itemExtent: 70,
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
                              width: 48,
                              height: 48,
                              decoration: BoxDecoration(color: c.section, borderRadius: BorderRadius.circular(12)),
                              child: Icon(Icons.music_note_rounded, color: c.accent),
                            ),
                            const SizedBox(width: 12),
                            Expanded(
                              child: Column(
                                mainAxisAlignment: MainAxisAlignment.center,
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Text(m.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w700)),
                                  Text(
                                    [if (m.album.isNotEmpty) m.album, formatDuration(context, m.durationMs ~/ 1000), formatBytes(context, m.size)].join(' · '),
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    style: TextStyle(fontSize: 13, color: c.muted, fontFeatures: tabular),
                                  ),
                                ],
                              ),
                            ),
                            const SizedBox(width: 8),
                            PickBox(on: on),
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
                  itemExtent: 66,
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
        itemExtent: 66,
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
                            Text(e.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w700)),
                            if (sub != null)
                              Text(sub, maxLines: 1, overflow: TextOverflow.ellipsis, style: TextStyle(color: c.muted, fontSize: 13, fontFeatures: tabular)),
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
                child: SizedBox.square(dimension: 56, child: Center(child: PickBox(on: on))),
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
