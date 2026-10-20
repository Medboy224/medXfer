/// What the phone holds, for the explorer by categories (WP-B.9 of the J6b plan): apps, media,
/// files. Android only, through the app's Kotlin (Library.kt); elsewhere [Library.supported] is
/// false and the tabs say so.
library;

import 'dart:io';
import 'dart:typed_data';

import 'file_picker.dart';
import 'folders.dart' show platformChannel;

enum MediaKind { images, video, audio }

class AppEntry {
  const AppEntry({required this.package, required this.label, required this.path, required this.size, this.split = false});

  factory AppEntry.fromMap(Map<Object?, Object?> m) =>
      AppEntry(package: '${m['package']}', label: '${m['label']}', path: '${m['path']}', size: (m['size'] as num?)?.toInt() ?? 0, split: m['split'] == true);

  final String package;
  final String label;

  /// The APK, sent as "<label>.apk".
  final String path;
  final int size;

  /// Installed in pieces: the base APK alone may not install on the other phone.
  final bool split;
}

class MediaEntry {
  const MediaEntry({
    required this.id,
    required this.kind,
    required this.name,
    required this.path,
    required this.size,
    this.album = '',
    this.modified = 0,
    this.durationMs = 0,
  });

  factory MediaEntry.fromMap(MediaKind kind, Map<Object?, Object?> m) => MediaEntry(
    id: (m['id'] as num).toInt(),
    kind: kind,
    name: '${m['name']}',
    path: '${m['path']}',
    size: (m['size'] as num?)?.toInt() ?? 0,
    album: '${m['album'] ?? ''}',
    modified: (m['modified'] as num?)?.toInt() ?? 0,
    durationMs: (m['duration'] as num?)?.toInt() ?? 0,
  );

  final int id;
  final MediaKind kind;
  final String name;
  final String path;
  final int size;

  /// The folder (photos, videos) or the album (music).
  final String album;

  /// Seconds since 1970.
  final int modified;
  final int durationMs;
}

class FileEntry {
  const FileEntry({required this.name, required this.path, required this.dir, this.size = 0, this.modified = 0});

  factory FileEntry.fromMap(Map<Object?, Object?> m) => FileEntry(
    name: '${m['name']}',
    path: '${m['path']}',
    dir: m['dir'] == true,
    size: (m['size'] as num?)?.toInt() ?? 0,
    modified: (m['modified'] as num?)?.toInt() ?? 0,
  );

  final String name;
  final String path;
  final bool dir;
  final int size;
  final int modified;
}

abstract interface class Library {
  bool get supported;

  Future<List<AppEntry>> apps();
  Future<Uint8List?> appIcon(String package, int size);

  /// One page, newest first.
  Future<List<MediaEntry>> media(MediaKind kind, {int offset = 0, int limit = 200});
  Future<Uint8List?> thumbnail(MediaKind kind, int id, int size);

  /// Whether photos, videos and music can be read; [askMedia] asks Android.
  Future<bool> mediaAllowed();
  Future<bool> askMedia();

  /// The storage's root, and one folder's entries (needs the access to all files).
  String get storageRoot;
  Future<List<FileEntry>> listDir(String path);

  /// Opens files by path for the core, each under the name given (STO-16).
  Future<List<OpenFile>> open(List<({String path, String name})> files);

  /// A file's thumbnail (image, video, PDF, APK's icon), px wide; null for other kinds.
  Future<Uint8List?> fileThumbnail(String path, int px);

  /// A folder's size, all its files.
  Future<int> dirSize(String path);

  /// The files of a category: documents, ebooks, apks, archives, large.
  Future<List<FileEntry>> files(FileCategory category, {int offset = 0, int limit = 200});
}

/// The classes of the Files tab, beside the folders.
enum FileCategory { documents, ebooks, apks, archives, large }

class NoLibrary implements Library {
  const NoLibrary();

  @override
  bool get supported => false;
  @override
  Future<List<AppEntry>> apps() async => const [];
  @override
  Future<Uint8List?> appIcon(String package, int size) async => null;
  @override
  Future<List<MediaEntry>> media(MediaKind kind, {int offset = 0, int limit = 200}) async => const [];
  @override
  Future<Uint8List?> thumbnail(MediaKind kind, int id, int size) async => null;
  @override
  Future<bool> mediaAllowed() async => false;
  @override
  Future<bool> askMedia() async => false;
  @override
  String get storageRoot => '';
  @override
  Future<List<FileEntry>> listDir(String path) async => const [];
  @override
  Future<List<OpenFile>> open(List<({String path, String name})> files) async => const [];
  @override
  Future<Uint8List?> fileThumbnail(String path, int px) async => null;
  @override
  Future<int> dirSize(String path) async => 0;
  @override
  Future<List<FileEntry>> files(FileCategory category, {int offset = 0, int limit = 200}) async => const [];
}

class AndroidLibrary implements Library {
  const AndroidLibrary();

  @override
  bool get supported => true;

  @override
  Future<List<AppEntry>> apps() async => [
    for (final m in await platformChannel.invokeListMethod<Map<Object?, Object?>>('libApps') ?? const []) AppEntry.fromMap(m),
  ];

  @override
  Future<Uint8List?> appIcon(String package, int size) => platformChannel.invokeMethod<Uint8List>('libAppIcon', {'package': package, 'size': size});

  @override
  Future<List<MediaEntry>> media(MediaKind kind, {int offset = 0, int limit = 200}) async => [
    for (final m
        in await platformChannel.invokeListMethod<Map<Object?, Object?>>('libMedia', {'kind': kind.name, 'offset': offset, 'limit': limit}) ?? const [])
      MediaEntry.fromMap(kind, m),
  ];

  @override
  Future<Uint8List?> thumbnail(MediaKind kind, int id, int size) =>
      platformChannel.invokeMethod<Uint8List>('libThumbnail', {'kind': kind.name, 'id': id, 'size': size});

  @override
  Future<bool> mediaAllowed() async => await platformChannel.invokeMethod<bool>('mediaAllowed') ?? false;

  @override
  Future<bool> askMedia() async => await platformChannel.invokeMethod<bool>('askMedia') ?? false;

  @override
  String get storageRoot => '/storage/emulated/0';

  @override
  Future<List<FileEntry>> listDir(String path) async => [
    for (final m in await platformChannel.invokeListMethod<Map<Object?, Object?>>('libListDir', {'path': path}) ?? const []) FileEntry.fromMap(m),
  ];

  @override
  Future<List<OpenFile>> open(List<({String path, String name})> files) async => [
    for (final m
        in await platformChannel.invokeListMethod<Map<Object?, Object?>>('libOpen', {
              'files': [
                for (final f in files) {'path': f.path, 'name': f.name},
              ],
            }) ??
            const [])
      OpenFile(fd: (m['fd'] as num).toInt(), name: '${m['name']}'),
  ];

  @override
  Future<Uint8List?> fileThumbnail(String path, int px) => platformChannel.invokeMethod<Uint8List>('libFileThumb', {'path': path, 'size': px});

  @override
  Future<int> dirSize(String path) async => (await platformChannel.invokeMethod<num>('libDirSize', {'path': path}))?.toInt() ?? 0;

  @override
  Future<List<FileEntry>> files(FileCategory category, {int offset = 0, int limit = 200}) async => [
    for (final m
        in await platformChannel.invokeListMethod<Map<Object?, Object?>>('libFiles', {'category': category.name, 'offset': offset, 'limit': limit}) ??
            const [])
      FileEntry.fromMap(m),
  ];
}

Library systemLibrary() => Platform.isAndroid ? const AndroidLibrary() : const NoLibrary();
