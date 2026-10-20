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

class Explorer extends ChangeNotifier {
  Explorer(this.library);

  final Library library;

  static const pageSize = 300;

  /// Thumbnails and icons kept, the most recent ones (about 30 MB at ~100 KB each).
  static const cacheSize = 300;

  List<AppEntry>? _apps;
  bool _appsLoading = false;
  final _media = {for (final k in MediaKind.values) k: MediaList()};
  bool? _mediaAllowed;
  String? _dir;
  List<FileEntry>? _entries;
  String? _dirError;

  final _images = <String, Uint8List>{}; // ordered: the most recent last
  final _pending = <String, Future<Uint8List?>>{};

  List<AppEntry>? get apps => _apps;
  MediaList media(MediaKind k) => _media[k]!;
  bool? get mediaAllowed => _mediaAllowed;
  String get dir => _dir ?? library.storageRoot;
  List<FileEntry>? get entries => _entries;
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

  /// A thumbnail or an icon, from the cache, else loaded once however many ask.
  Future<Uint8List?> image(String key, Future<Uint8List?> Function() load) {
    final hit = _images.remove(key);
    if (hit != null) {
      _images[key] = hit; // the most recent last
      return SynchronousFuture(hit);
    }
    return _pending[key] ??= load()
        .then((bytes) {
          if (bytes != null) {
            _images[key] = bytes;
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
        .whenComplete(() {
          // A block, not an arrow: remove() gives back this very future, and whenComplete would
          // wait for it, that is for itself, forever.
          _pending.remove(key);
        });
  }

  /// The picks of a tab's items, for "select all": 1 Apps, 2 Photos, 3 Videos, 4 Music, 5 Files.
  List<Pick> picksOf(int tab) => switch (tab) {
    1 => [for (final a in _apps ?? const <AppEntry>[]) pickApp(a)],
    2 => [for (final m in media(MediaKind.images).items) pickMedia(m)],
    3 => [for (final m in media(MediaKind.video).items) pickMedia(m)],
    4 => [for (final m in media(MediaKind.audio).items) pickMedia(m)],
    5 => [for (final e in _entries ?? const <FileEntry>[]) pickFile(e)],
    _ => const [],
  };

  static Pick pickApp(AppEntry a) => Pick(path: a.path, name: '${a.label}.apk', size: a.size, kind: PickKind.app);
  static Pick pickMedia(MediaEntry m) => Pick(path: m.path, name: m.name, size: m.size, kind: PickKind.media);
  static Pick pickFile(FileEntry e) => Pick(path: e.path, name: e.name, size: e.size, kind: e.dir ? PickKind.folder : PickKind.file);
}
