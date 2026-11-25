/// What the phone holds, for the explorer by categories (WP-B.9 of the J6b plan): apps, media,
/// files. Android only, through the app's Kotlin (Library.kt); elsewhere [Library.supported] is
/// false and the tabs say so.
library;

import 'dart:async';
import 'dart:io';
import 'dart:isolate';

import 'package:flutter/services.dart';

import 'file_picker.dart';
import 'folders.dart' show platformChannel;

enum MediaKind { images, video, audio }

/// What the media are sorted by.
enum MediaSort { date, name, size }

/// The order and the search of a list of media (plan J8, WP-8.1b): done by the platform, over
/// the whole library.
class MediaOrder {
  const MediaOrder({this.sort = MediaSort.date, this.descending = true, this.search = ''});

  final MediaSort sort;

  /// Newest, last in the alphabet, or largest first.
  final bool descending;

  /// Kept: the media whose name or album holds it; empty: all.
  final String search;

  MediaOrder copyWith({MediaSort? sort, bool? descending, String? search}) =>
      MediaOrder(sort: sort ?? this.sort, descending: descending ?? this.descending, search: search ?? this.search);

  @override
  bool operator ==(Object other) => other is MediaOrder && other.sort == sort && other.descending == descending && other.search == search;

  @override
  int get hashCode => Object.hash(sort, descending, search);
}

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
    this.width = 0,
    this.height = 0,
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
    width: (m['width'] as num?)?.toInt() ?? 0,
    height: (m['height'] as num?)?.toInt() ?? 0,
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

  /// As shown, turned already; 0 when unknown.
  final int width, height;

  /// Width over height; 0 when unknown (the mosaic shows a square).
  double get aspect => width > 0 && height > 0 ? width / height : 0;
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

  /// One page, in [order] (newest first by default).
  Future<List<MediaEntry>> media(MediaKind kind, {int offset = 0, int limit = 200, MediaOrder order = const MediaOrder()});

  /// The media of [kind] at these [paths], those the system's library knows (in no order): a
  /// queue made from a folder of the Files tab.
  Future<List<MediaEntry>> mediaAt(MediaKind kind, List<String> paths);
  Future<Uint8List?> thumbnail(MediaKind kind, int id, int size);

  /// A tile's image, [height] px tall (less for a wide one, at most [width] px wide), in its
  /// own proportions, read from the file itself: sharp where [thumbnail] stops short (about
  /// 270 px on Android), slower the first time (then kept). Null when there is none, or, with
  /// [keptOnly], when it was not made yet (then nothing is made).
  Future<Uint8List?> sharpThumbnail(MediaEntry m, {required int height, required int width, bool keptOnly = false});

  /// Makes the sharp thumbnails ahead, in the background, newest first ([on]), or stops.
  Future<void> prepareSharp({required int height, required int width, required bool on});

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

  /// A photo Flutter would decode on the thread that draws ([decodedApart]), decoded away from
  /// it, at least width × height px, as JPEG; null where this library does not.
  Future<Uint8List?> decodePhoto(String path, int width, int height);

  /// A folder's size, all its files.
  Future<int> dirSize(String path);

  /// The files of a category: documents, ebooks, apks, archives, large.
  Future<List<FileEntry>> files(FileCategory category, {int offset = 0, int limit = 200});

  /// The device's content changed: a photo taken, a file added or deleted.
  Stream<void> get changes;
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
  Future<List<MediaEntry>> media(MediaKind kind, {int offset = 0, int limit = 200, MediaOrder order = const MediaOrder()}) async => const [];
  @override
  Future<List<MediaEntry>> mediaAt(MediaKind kind, List<String> paths) async => const [];
  @override
  Future<Uint8List?> thumbnail(MediaKind kind, int id, int size) async => null;
  @override
  Future<Uint8List?> sharpThumbnail(MediaEntry m, {required int height, required int width, bool keptOnly = false}) async => null;
  @override
  Future<void> prepareSharp({required int height, required int width, required bool on}) async {}
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
  Future<Uint8List?> decodePhoto(String path, int width, int height) async => null;

  /// A desktop: the folder is walked here, away from the screen's isolate (the basket).
  @override
  Future<int> dirSize(String path) => Isolate.run(() => _walk(path));
  @override
  Future<List<FileEntry>> files(FileCategory category, {int offset = 0, int limit = 200}) async => const [];
  @override
  Stream<void> get changes => const Stream.empty();
}

/// The bytes of the files under [root]; a folder that cannot be read counts for nothing, the
/// others still count.
int _walk(String root) {
  var total = 0;
  final pending = [root];
  while (pending.isNotEmpty) {
    try {
      for (final e in Directory(pending.removeLast()).listSync(followLinks: false)) {
        if (e is Directory) {
          pending.add(e.path);
        } else if (e is File) {
          total += e.statSync().size;
        }
      }
    } on FileSystemException {
      // Not readable: skipped.
    }
  }
  return total;
}

class AndroidLibrary implements Library {
  const AndroidLibrary();

  static const _events = MethodChannel('dev.medxfer.app/library');
  static final StreamController<void> _changes = StreamController<void>.broadcast(
    onListen: () => _events.setMethodCallHandler((call) async {
      if (call.method == 'changed') _changes.add(null);
    }),
  );

  /// Library.kt watches MediaStore and calls changed, once things settle.
  @override
  Stream<void> get changes => _changes.stream;

  @override
  bool get supported => true;

  @override
  Future<List<AppEntry>> apps() async => [
    for (final m in await platformChannel.invokeListMethod<Map<Object?, Object?>>('libApps') ?? const []) AppEntry.fromMap(m),
  ];

  @override
  Future<Uint8List?> appIcon(String package, int size) => platformChannel.invokeMethod<Uint8List>('libAppIcon', {'package': package, 'size': size});

  @override
  Future<List<MediaEntry>> media(MediaKind kind, {int offset = 0, int limit = 200, MediaOrder order = const MediaOrder()}) async => [
    for (final m
        in await platformChannel.invokeListMethod<Map<Object?, Object?>>('libMedia', {
              'kind': kind.name,
              'offset': offset,
              'limit': limit,
              'sort': order.sort.name,
              'descending': order.descending,
              'search': order.search,
            }) ??
            const [])
      MediaEntry.fromMap(kind, m),
  ];

  @override
  Future<List<MediaEntry>> mediaAt(MediaKind kind, List<String> paths) async => [
    for (final m in await platformChannel.invokeListMethod<Map<Object?, Object?>>('libMediaAt', {'kind': kind.name, 'paths': paths}) ?? const [])
      MediaEntry.fromMap(kind, m),
  ];

  @override
  Future<Uint8List?> thumbnail(MediaKind kind, int id, int size) =>
      platformChannel.invokeMethod<Uint8List>('libThumbnail', {'kind': kind.name, 'id': id, 'size': size});

  @override
  Future<Uint8List?> sharpThumbnail(MediaEntry m, {required int height, required int width, bool keptOnly = false}) => platformChannel.invokeMethod<Uint8List>(
    'libSharpThumbnail',
    {'kind': m.kind.name, 'id': m.id, 'height': height, 'width': width, 'modified': m.modified, 'keptOnly': keptOnly},
  );

  @override
  Future<void> prepareSharp({required int height, required int width, required bool on}) =>
      platformChannel.invokeMethod<void>('libPrepareSharp', {'height': height, 'width': width, 'on': on});

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
  Future<Uint8List?> decodePhoto(String path, int width, int height) =>
      platformChannel.invokeMethod<Uint8List>('libDecodePhoto', {'path': path, 'width': width, 'height': height});

  @override
  Future<int> dirSize(String path) async => (await platformChannel.invokeMethod<num>('libDirSize', {'path': path}))?.toInt() ?? 0;

  @override
  Future<List<FileEntry>> files(FileCategory category, {int offset = 0, int limit = 200}) async => [
    for (final m
        in await platformChannel.invokeListMethod<Map<Object?, Object?>>('libFiles', {'category': category.name, 'offset': offset, 'limit': limit}) ?? const [])
      FileEntry.fromMap(m),
  ];
}

Library systemLibrary() => Platform.isAndroid ? const AndroidLibrary() : const NoLibrary();

/// The kind of media a file is, by its name; null for any other file (the Files tab).
MediaKind? mediaKindOf(String name) {
  final dot = name.lastIndexOf('.');
  if (dot < 0) return null;
  return switch (name.substring(dot + 1).toLowerCase()) {
    'jpg' || 'jpeg' || 'png' || 'webp' || 'gif' || 'heic' || 'heif' || 'bmp' || 'avif' => MediaKind.images,
    'mp4' || 'mkv' || 'avi' || 'mov' || '3gp' || 'webm' || 'wmv' || 'flv' || 'm4v' || 'ts' || 'mpg' || 'mpeg' || 'divx' || 'asf' || 'vob' || 'ogv' => MediaKind.video,
    'mp3' || 'm4a' || 'aac' || 'flac' || 'ogg' || 'opus' || 'wav' || 'wma' || 'amr' || 'mka' || 'ape' || 'ac3' => MediaKind.audio,
    _ => null,
  };
}

/// The photos Flutter decodes through Android, on its platform thread, which is also the one
/// that draws (merged since Flutter 3.29): HEIC, HEIF, AVIF. [Library.decodePhoto] decodes them.
bool decodedApart(String path) {
  final dot = path.lastIndexOf('.');
  return dot >= 0 && const {'heic', 'heif', 'avif'}.contains(path.substring(dot + 1).toLowerCase());
}
