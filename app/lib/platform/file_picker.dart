/// Choosing what to send on a desktop (WP-B.3): files or a folder, by path, never copied
/// (STO-16). The file_selector package (BSD, by the Flutter team) is this interface's adapter.
/// Android chooses in the application's own explorer (WP-B.9), or by descriptor (WP-B.4).
library;

import 'dart:io';

import 'package:file_selector/file_selector.dart' as fs;

abstract interface class FilePicker {
  bool get supported;

  /// The paths of the files chosen; empty when the user cancels.
  Future<List<String>> files();

  /// The path of the folder chosen; null when the user cancels.
  Future<String?> folder();
}

class NoFilePicker implements FilePicker {
  const NoFilePicker();

  @override
  bool get supported => false;

  @override
  Future<List<String>> files() async => const [];

  @override
  Future<String?> folder() async => null;
}

class SelectorFilePicker implements FilePicker {
  const SelectorFilePicker();

  @override
  bool get supported => Platform.isWindows || Platform.isLinux || Platform.isMacOS;

  @override
  Future<List<String>> files() async => [for (final f in await fs.openFiles()) f.path];

  @override
  Future<String?> folder() => fs.getDirectoryPath();
}

FilePicker systemFilePicker() => const SelectorFilePicker().supported ? const SelectorFilePicker() : const NoFilePicker();
