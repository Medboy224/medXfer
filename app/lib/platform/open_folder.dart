/// "Open the folder" of a received transfer: the system's file manager, given the folder as one
/// argument (never a command built from text); on Android, the Files app on that folder.
library;

import 'dart:io';

import 'folders.dart' show platformChannel;

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

/// Android: the system's file manager on a folder of the shared storage (Storage.kt).
class AndroidFolderOpener implements FolderOpener {
  const AndroidFolderOpener();

  @override
  bool get supported => true;

  @override
  Future<void> open(String folder) async {
    try {
      await platformChannel.invokeMethod<bool>('openFolder', {'path': folder});
    } on Object {
      // No file manager takes it: nothing more to do, the files are there all the same.
    }
  }
}

FolderOpener systemFolderOpener() => Platform.isAndroid ? const AndroidFolderOpener() : const SystemFolderOpener();
