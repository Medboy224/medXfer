/// "Open the folder" of a received transfer: the system's file manager, given the folder as one
/// argument (never a command built from text). Android opens its folders with WP-B.4.
library;

import 'dart:io';

abstract interface class FolderOpener {
  bool get supported;
  Future<void> open(String folder);
}

class SystemFolderOpener implements FolderOpener {
  const SystemFolderOpener();

  @override
  bool get supported => Platform.isWindows || Platform.isLinux || Platform.isMacOS;

  @override
  Future<void> open(String folder) async {
    if (folder.isEmpty) return;
    final tool = Platform.isWindows ? 'explorer.exe' : (Platform.isMacOS ? 'open' : 'xdg-open');
    await Process.start(tool, [folder], mode: ProcessStartMode.detached);
  }
}

class NoFolderOpener implements FolderOpener {
  const NoFolderOpener();

  @override
  bool get supported => false;

  @override
  Future<void> open(String folder) async {}
}
