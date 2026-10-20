/// Choosing what to send (WP-B.3, WP-B.4), never copied (STO-16). On a desktop, files or a
/// folder by path: the file_selector package (BSD, by the Flutter team) is the adapter. On
/// Android, the system's picker: each file opened by the app's Kotlin, its descriptor handed to
/// the core, which closes it. Folders on Android come with the explorer of WP-B.9.
library;

import 'dart:io';

import 'package:file_selector/file_selector.dart' as fs;

import 'folders.dart' show platformChannel;

/// A file already open: its descriptor and the name the other device sees.
class OpenFile {
  const OpenFile({required this.fd, required this.name});

  final int fd;
  final String name;

  Map<String, Object?> toJson() => {'fd': fd, 'name': name};
}

/// What the user chose: paths (desktops), or open files (Android).
class Picked {
  const Picked({this.paths = const [], this.files = const []});

  final List<String> paths;
  final List<OpenFile> files;

  bool get isEmpty => paths.isEmpty && files.isEmpty;
}

abstract interface class FilePicker {
  bool get supported;

  /// A folder can be chosen whole (desktops).
  bool get folders;

  /// The files chosen; empty when the user cancels.
  Future<Picked> files();

  /// The path of the folder chosen; null when the user cancels, or without [folders].
  Future<String?> folder();
}

class NoFilePicker implements FilePicker {
  const NoFilePicker();

  @override
  bool get supported => false;

  @override
  bool get folders => false;

  @override
  Future<Picked> files() async => const Picked();

  @override
  Future<String?> folder() async => null;
}

class SelectorFilePicker implements FilePicker {
  const SelectorFilePicker();

  @override
  bool get supported => Platform.isWindows || Platform.isLinux || Platform.isMacOS;

  @override
  bool get folders => true;

  @override
  Future<Picked> files() async => Picked(paths: [for (final f in await fs.openFiles()) f.path]);

  @override
  Future<String?> folder() => fs.getDirectoryPath();
}

/// The system's picker, through the app's Kotlin (Storage.kt).
class AndroidFilePicker implements FilePicker {
  const AndroidFilePicker();

  @override
  bool get supported => true;

  @override
  bool get folders => false;

  @override
  Future<Picked> files() async {
    final list = await platformChannel.invokeListMethod<Map<Object?, Object?>>('pickFiles') ?? const [];
    return Picked(files: [
      for (final m in list) OpenFile(fd: (m['fd'] as num).toInt(), name: '${m['name']}'),
    ]);
  }

  @override
  Future<String?> folder() async => null;
}

FilePicker systemFilePicker() {
  if (Platform.isAndroid) return const AndroidFilePicker();
  return const SelectorFilePicker().supported ? const SelectorFilePicker() : const NoFilePicker();
}
