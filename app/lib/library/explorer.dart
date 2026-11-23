/// The explorer's lists (WP-B.9): apps, photos, videos, music, and the folder open in Files.
/// Loaded on first view, media by pages of [pageSize] as the grid scrolls; thumbnails kept in a
/// bounded memory cache (UI-1: smooth with thousands of photos, little memory).
library;

import 'dart:async';
import 'dart:math' as math;

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
  Explorer(this.library) {
    _changes = library.changes.listen((_) => unawaited(refresh()));
  }

  final Library library;
  StreamSubscription<void>? _changes;
  bool _disposed = false;
  bool _refreshing = false;
  bool _again = false;

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

  final _orders = {for (final k in MediaKind.values) k: const MediaOrder()};
  final _columns = {MediaKind.images: 3, MediaKind.video: 2, MediaKind.audio: 1};
  final _asList = {MediaKind.images: false, MediaKind.video: false, MediaKind.audio: true};

  List<AppEntry>? get apps => _apps;
  MediaList media(MediaKind k) => _media[k]!;

  /// The order and search of a kind of media.
  MediaOrder order(MediaKind k) => _orders[k]!;

  /// A new order or search: the list loads again from its start, in that order.
  void setOrder(MediaKind k, MediaOrder o) {
    if (_orders[k] == o) return;
    _orders[k] = o;
    _media[k] = MediaList();
    notifyListeners();
    unawaited(more(k));
  }

  /// Columns of a grid (2 to 5), or a list.
  int columns(MediaKind k) => _columns[k]!;
  bool asList(MediaKind k) => _asList[k]!;

  static const minColumns = 2;
  static const maxColumns = 5;

  /// [quiet]: kept without a word to the listeners, during a pinch: each step rebuilt every tab
  /// in the middle of the gesture (real test of 18 November 2026); said once it is over.
  void setLayout(MediaKind k, {int? columns, bool? asList, bool quiet = false}) {
    final c = (columns ?? _columns[k]!).clamp(minColumns, maxColumns);
    final l = asList ?? _asList[k]!;
    if (c == _columns[k] && l == _asList[k]) return;
    _columns[k] = c;
    _asList[k] = l;
    if (!quiet) notifyListeners();
  }

  /// Says the layouts changed quietly ([setLayout]) to the listeners.
  void layoutSettled() => notifyListeners();
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
      final page = await library.media(kind, offset: m.items.length, limit: pageSize, order: _orders[kind]!);
      // Another order asked meanwhile: this page belongs to a list no longer shown.
      if (!identical(_media[kind], m)) return;
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

  /// The device's content changed (a photo taken, a file added, an app installed), or the app
  /// comes back to the screen: what was loaded is read again, and replaced only where it
  /// differs; the scroll stays (real test of 22 November 2026: a screenshot showed only once the
  /// app restarted). Asked again while it reads, it reads once more after.
  Future<void> refresh() async {
    if (_refreshing) {
      _again = true;
      return;
    }
    _refreshing = true;
    try {
      do {
        _again = false;
        var changed = false;
        for (final k in MediaKind.values) {
          changed = await _reloadMedia(k) || changed;
        }
        for (final c in FileCategory.values) {
          changed = await _reloadFiles(c) || changed;
        }
        changed = await _reloadDir() || changed;
        changed = await _reloadApps() || changed;
        if (changed && !_disposed) notifyListeners();
      } while (_again && !_disposed);
    } on Object catch (e) {
      if (kDebugMode) debugPrint('explorer: refresh failed: $e');
    } finally {
      _refreshing = false;
    }
  }

  static bool _same<T>(List<T> a, List<T> b, bool Function(T, T) equal) {
    if (a.length != b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (!equal(a[i], b[i])) return false;
    }
    return true;
  }

  /// A kind of media as far as it was loaded; a kind never shown loads when it is.
  Future<bool> _reloadMedia(MediaKind k) async {
    final m = _media[k]!;
    if ((m.items.isEmpty && !m.done) || m.loading || m.error != null || _disposed) return false;
    final want = math.max(m.items.length, pageSize);
    final items = await library.media(k, limit: want, order: _orders[k]!);
    // Another order, or a page, meanwhile: that list reads the device as it is.
    if (!identical(_media[k], m) || m.loading) return false;
    if (_same(items, m.items, (a, b) => a.id == b.id && a.name == b.name && a.size == b.size && a.modified == b.modified)) return false;
    _media[k] = MediaList()
      ..items.addAll(items)
      ..done = items.length < want;
    return true;
  }

  static bool _sameFile(FileEntry a, FileEntry b) => a.path == b.path && a.name == b.name && a.size == b.size && a.dir == b.dir;

  Future<bool> _reloadFiles(FileCategory c) async {
    final f = _files[c]!;
    if ((f.items.isEmpty && !f.done) || f.loading || f.error != null || _disposed) return false;
    final want = math.max(f.items.length, pageSize);
    final items = await library.files(c, limit: want);
    if (!identical(_files[c], f) || f.loading) return false;
    if (_same(items, f.items, _sameFile)) return false;
    _files[c] = FileList()
      ..items.addAll(items)
      ..done = items.length < want;
    return true;
  }

  /// The folder open in Files.
  Future<bool> _reloadDir() async {
    final before = _entries;
    if (before == null || _dirError != null || _disposed) return false;
    final dir = this.dir;
    final entries = await library.listDir(dir);
    if (dir != this.dir || !identical(_entries, before)) return false; // another folder opened meanwhile
    if (_same(entries, before, _sameFile)) return false;
    _entries = entries;
    return true;
  }

  Future<bool> _reloadApps() async {
    final before = _apps;
    if (before == null || _appsLoading || _disposed) return false;
    final apps = await library.apps();
    if (!identical(_apps, before)) return false;
    if (_same(apps, before, (a, b) => a.package == b.package && a.label == b.label && a.size == b.size)) return false;
    _apps = apps;
    return true;
  }

  @override
  void notifyListeners() {
    if (!_disposed) super.notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    unawaited(_changes?.cancel());
    super.dispose();
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

  static Pick pickApp(AppEntry a) => Pick(path: a.path, name: '${a.label}.apk', size: a.size, kind: PickKind.app, thumb: 'app:${a.package}');
  static Pick pickMedia(MediaEntry m) => Pick(path: m.path, name: m.name, size: m.size, kind: PickKind.media, thumb: '${m.kind.name}:${m.id}');
  static Pick pickFile(FileEntry e) =>
      Pick(path: e.path, name: e.name, size: e.size, kind: e.dir ? PickKind.folder : PickKind.file, thumb: e.dir ? null : 'file:${e.path}');
}
