/// Where received files go on Android (WP-B.4, decision J2 of the J6b plan): medXfer/ at the
/// root of the shared storage, sorted by type, with the access to all files; Download/medXfer/
/// without it. The desktops keep the core's folder (Downloads/medXfer).
library;

import 'dart:io';

import 'folders.dart' show platformChannel;

/// The folders of medXfer/, by type (J2). Their names stay the same whatever the language: a
/// change of language must not split the received files.
const typeFolders = (apps: 'Apps', photos: 'Photos', videos: 'Vidéos', music: 'Musique', files: 'Fichiers');

const _photos = {'jpg', 'jpeg', 'png', 'gif', 'webp', 'heic', 'heif', 'bmp', 'tif', 'tiff', 'dng', 'avif'};
const _videos = {'mp4', 'mkv', 'mov', 'avi', 'webm', '3gp', 'm4v', 'ts', 'wmv'};
const _music = {'mp3', 'm4a', 'aac', 'flac', 'wav', 'ogg', 'opus', 'wma', 'amr', 'mid'};
const _apps = {'apk', 'apks', 'xapk', 'apkm'};

/// The folder of one file, by its extension.
String typeFolderOf(String path) {
  final name = path.replaceAll('\\', '/').split('/').last.toLowerCase();
  final dot = name.lastIndexOf('.');
  final ext = dot < 0 ? '' : name.substring(dot + 1);
  if (_photos.contains(ext)) return typeFolders.photos;
  if (_videos.contains(ext)) return typeFolders.videos;
  if (_music.contains(ext)) return typeFolders.music;
  if (_apps.contains(ext)) return typeFolders.apps;
  return typeFolders.files;
}

/// The folder of an offer: its files' type when they all share one, else Fichiers. An offer
/// stays whole in one folder: a folder sent is never split.
String typeFolderFor(List<String> paths) {
  if (paths.isEmpty) return typeFolders.files;
  final first = typeFolderOf(paths.first);
  return paths.every((p) => typeFolderOf(p) == first) ? first : typeFolders.files;
}

abstract interface class ReceiveStorage {
  /// Android: the received files are sorted, and the access to all files may be asked.
  bool get supported;

  /// Whether the access to all files is granted (medXfer/ at the root).
  Future<bool> allFiles();

  /// Opens the system's page of that access; true once granted.
  Future<bool> askAllFiles();

  /// medXfer/ at the root, or Download/medXfer/.
  Future<String> root();
}

class NoReceiveStorage implements ReceiveStorage {
  const NoReceiveStorage();

  @override
  bool get supported => false;

  @override
  Future<bool> allFiles() async => true;

  @override
  Future<bool> askAllFiles() async => true;

  @override
  Future<String> root() async => '';
}

class AndroidReceiveStorage implements ReceiveStorage {
  const AndroidReceiveStorage();

  @override
  bool get supported => true;

  @override
  Future<bool> allFiles() async => await platformChannel.invokeMethod<bool>('allFiles') ?? false;

  @override
  Future<bool> askAllFiles() async => await platformChannel.invokeMethod<bool>('askAllFiles') ?? false;

  @override
  Future<String> root() async => (await platformChannel.invokeMethod<String>('downloadDir'))!;
}

ReceiveStorage systemReceiveStorage() => Platform.isAndroid ? const AndroidReceiveStorage() : const NoReceiveStorage();
