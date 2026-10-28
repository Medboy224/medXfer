/// Paths chosen on a desktop, by its pickers or dropped on the window, as picks of the basket
/// (WP-B.8): files and folders together go in one transfer, by path. The system's dialog
/// chooses files or a folder, never both at once.
library;

import 'dart:io';

import 'package:path/path.dart' as p;

import 'selection.dart';

List<Pick> picksOfPaths(Iterable<String> paths) => [
  for (final path in paths)
    if (path.isNotEmpty) _pick(path),
];

Pick _pick(String path) {
  final stat = FileStat.statSync(path);
  final folder = stat.type == FileSystemEntityType.directory;
  // A folder's size comes once walked (Explorer.sizeFolders).
  return Pick(path: path, name: p.basename(path), size: folder || stat.size < 0 ? 0 : stat.size, kind: folder ? PickKind.folder : PickKind.file);
}
