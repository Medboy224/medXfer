/// The explorer's lists (WP-B.9): apps, photos, videos, music, and the folder open in Files.
/// Loaded on first view, media by pages of [pageSize] as the grid scrolls; thumbnails kept in a
/// bounded memory cache (UI-1: smooth with thousands of photos, little memory).
library;

import 'dart:async';

import 'package:flutter/foundation.dart';

import '../platform/library.dart';
import 'selection.dart';

class MediaList {
  final items = <MediaEntry>[];
  bool loading = false;
  bool done = false;
  String? error;
}

/// The files of one category of the Files tab.
class FileList {
  final items = <FileEntry>[];
  bool loading = false;
  bool done = false;
  String? error;
}

/// One image to load, waiting for its turn.
class _Load {
  _Load(this.key, this.load);

  final String key;
  final Future<Uint8List?> Function() load;
  final done = Completer<Uint8List?>();
}

class Explorer extends ChangeNotifier {
  Explorer(this.library);

  final Library library;

  static const pageSize = 300;

  /// Thumbnails and icons kept, the most recent ones (about 30 MB at ~100 KB each).
  static const cacheSize = 300;

  /// Images loading at once. The rest wait, the last asked first: after a fling, the tiles on
  /// screen come before those scrolled past, which are dropped once gone ([forget]).
  static const parallel = 6;

  List<AppEntry>? _apps;
  bool _appsLoading = false;
  final _media = {for (final k in MediaKind.values) k: MediaList()};
  bool? _mediaAllowed;
  String? _dir;
  List<FileEntry>? _entries;
  String? _dirError;

  final _images = <String, Uint8List>{}; // ordered: the most recent last
  final _pending = <String, Future<Uint8List?>>{};
  final _waiting = <_Load>[];
  int _running = 0;

  /// The Files tab: the folders, or one category of files.
  FileCategory? _category;
  final _files = {for (final c in FileCategory.values) c: FileList()};
  final _sizing = <String>{};

  List<AppEntry>? get apps => _apps;
  MediaList media(MediaKind k) => _media[k]!;
  bool? get mediaAllowed => _mediaAllowed;
  String get dir => _dir ?? library.storageRoot;
  List<FileEntry>? get entries => _entries;
  FileCategory? get category => _category;
  FileList files(FileCategory c) => _files[c]!;

  /// Shows a category of files, or the folders (null).
  void showCategory(FileCategory? c) {
    if (_category == c) return;
    _category = c;
    notifyListeners();
    if (c != null && files(c).items.isEmpty) unawaited(moreFiles(c));
  }

  /// The next page of a category, if any.
  Future<void> moreFiles(FileCategory c) async {
    final f = _files[c]!;
    if (f.loading || f.done) return;
    f.loading = true;
    try {
      final page = await library.files(c, offset: f.items.length, limit: pageSize);
      f.items.addAll(page);
      f.done = page.length < pageSize;
      f.error = null;
    } on Object catch (e) {
      f.error = '$e';
      f.done = true;
    } finally {
      f.loading = false;
      notifyListeners();
    }
  }

  /// Gives the picked folders their size once known: the basket then counts them.
  void sizeFolders(Selection selection) {
    for (final p in selection.items.where((p) => p.kind == PickKind.folder && p.size == 0).toList()) {
      if (!_sizing.add(p.path)) continue;
      library.dirSize(p.path).then((size) => selection.setSize(p.path, size), onError: (Object _) {}).whenComplete(() {
        _sizing.remove(p.path);
      });
    }
  }

  String? get dirError => _dirError;

  Future<void> loadApps() async {
    if (_apps != null || _appsLoading) return;
    _appsLoading = true;
    try {
      _apps = await library.apps();
    } finally {
      _appsLoading = false;
      notifyListeners();
    }
  }

  Future<void> checkMedia() async {
    _mediaAllowed = await library.mediaAllowed();
    notifyListeners();
  }

  Future<void> askMedia() async {
    _mediaAllowed = await library.askMedia();
    notifyListeners();
  }

  /// The next page of a kind of media, if any.
  Future<void> more(MediaKind kind) async {
    final m = _media[kind]!;
    if (m.loading || m.done) return;
    // No notice yet: it may be called while a tab builds; the answer notifies.
    m.loading = true;
    try {
      final page = await library.media(kind, offset: m.items.length, limit: pageSize);
      m.items.addAll(page);
      if (kDebugMode) debugPrint('explorer: ${kind.name} +${page.length} (${m.items.length})');
      m.done = page.length < pageSize;
      m.error = null;
    } on Object catch (e) {
      if (kDebugMode) debugPrint('explorer: ${kind.name} failed: $e');
      m.error = '$e';
      m.done = true;
    } finally {
      m.loading = false;
      notifyListeners();
    }
  }

  Future<void> open(String path) async {
    _dir = path;
    _entries = null;
    _dirError = null;
    notifyListeners();
    try {
      _entries = await library.listDir(path);
    } on Object catch (e) {
      _dirError = '$e';
      _entries = const [];
    }
    notifyListeners();
  }

  /// The folder above the open one, or null at the storage's root.
  String? get parent {
    final d = dir;
    if (d == library.storageRoot || !d.contains('/')) return null;
    return d.substring(0, d.lastIndexOf('/'));
  }

  /// A thumbnail or an icon already in the cache, made the most recent; null otherwise.
  Uint8List? cached(String key) {
    final hit = _images.remove(key);
    if (hit != null) _images[key] = hit;
    return hit;
  }

  /// A thumbnail or an icon, from the cache, else loaded once however many ask, in its turn.
  Future<Uint8List?> image(String key, Future<Uint8List?> Function() load) {
    final hit = cached(key);
    if (hit != null) return SynchronousFuture(hit);
    final pending = _pending[key];
    if (pending != null) return pending;
    final job = _Load(key, load);
    _waiting.add(job);
    _pending[key] = job.done.future;
    _pump();
    return job.done.future;
  }

  /// A tile left the screen before its image was loaded: it is not loaded.
  void forget(String key) {
    final i = _waiting.indexWhere((j) => j.key == key);
    if (i < 0) return;
    final job = _waiting.removeAt(i);
    _pending.remove(key);
    job.done.complete(null);
  }

  void _pump() {
    while (_running < parallel && _waiting.isNotEmpty) {
      final job = _waiting.removeLast(); // the last asked: on screen now
      _running++;
      job
          .load()
          .then((bytes) {
            if (bytes != null) {
              _images[job.key] = bytes;
              while (_images.length > cacheSize) {
                _images.remove(_images.keys.first);
              }
            }
            return bytes;
          })
          .catchError((Object e) {
            if (kDebugMode) debugPrint('explorer: image failed: $e');
            return null;
          })
          .then((bytes) {
            _pending.remove(job.key);
            _running--;
            job.done.complete(bytes);
            _pump();
          });
    }
  }

  /// The picks of a tab's items, for "select all": 1 Apps, 2 Photos, 3 Videos, 4 Music, 5 Files.
  List<Pick> picksOf(int tab) => switch (tab) {
    1 => [for (final a in _apps ?? const <AppEntry>[]) pickApp(a)],
    2 => [for (final m in media(MediaKind.images).items) pickMedia(m)],
    3 => [for (final m in media(MediaKind.video).items) pickMedia(m)],
    4 => [for (final m in media(MediaKind.audio).items) pickMedia(m)],
    5 => [for (final e in _category == null ? _entries ?? const <FileEntry>[] : files(_category!).items) pickFile(e)],
    _ => const [],
  };

  static Pick pickApp(AppEntry a) => Pick(path: a.path, name: '${a.label}.apk', size: a.size, kind: PickKind.app);
  static Pick pickMedia(MediaEntry m) => Pick(path: m.path, name: m.name, size: m.size, kind: PickKind.media);
  static Pick pickFile(FileEntry e) => Pick(path: e.path, name: e.name, size: e.size, kind: e.dir ? PickKind.folder : PickKind.file);
}
