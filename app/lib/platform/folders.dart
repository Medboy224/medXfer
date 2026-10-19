/// Where the app keeps its data and where received files go (B6: the core takes its paths
/// from the interface). No plugin: on Android, the app's own Kotlin answers on a channel; on
/// the desktop, the system's usual folders.
library;

import 'dart:io';

import 'package:flutter/services.dart';
import 'package:path/path.dart' as p;

/// The app's folders.
abstract interface class Folders {
  /// Configuration, identity, trusted devices, reports: private to the app.
  Future<String> dataDir();

  /// Where received files go by default.
  Future<String> downloadDir();
}

/// The app's identifier (decision J3 of the J6b plan, provisional): its data folder's name on
/// the desktop.
const appId = 'dev.medxfer.app';

/// The channel of the app's Kotlin (android/app/src/main/kotlin/dev/medxfer/app).
const platformChannel = MethodChannel('dev.medxfer.app/platform');

class SystemFolders implements Folders {
  const SystemFolders();

  @override
  Future<String> dataDir() async {
    if (Platform.isAndroid) {
      return (await platformChannel.invokeMethod<String>('dataDir'))!;
    }
    // Named after the app's identifier: never the folder of the xfer command line
    // (%APPDATA%\medxfer, ~/.config/medxfer), whose identity and trusted devices are its own.
    if (Platform.isWindows) {
      return p.join(Platform.environment['APPDATA'] ?? _home(), appId);
    }
    final xdg = Platform.environment['XDG_DATA_HOME'];
    return p.join(xdg != null && xdg.isNotEmpty ? xdg : p.join(_home(), '.local', 'share'), appId);
  }

  /// On the desktop, a medXfer folder in the user's downloads. On Android, until WP-B.4 sets
  /// medXfer/ at the root of the shared storage (STO-15, J2), the app's own external folder.
  @override
  Future<String> downloadDir() async {
    if (Platform.isAndroid) {
      return (await platformChannel.invokeMethod<String>('downloadDir'))!;
    }
    return p.join(_downloads(), 'medXfer');
  }

  static String _home() =>
      Platform.environment[Platform.isWindows ? 'USERPROFILE' : 'HOME'] ?? Directory.systemTemp.path;

  /// The user's downloads: XDG_DOWNLOAD_DIR on Linux (~/.config/user-dirs.dirs), else
  /// ~/Downloads.
  static String _downloads() {
    final home = _home();
    if (Platform.isLinux) {
      final dirs = File(p.join(home, '.config', 'user-dirs.dirs'));
      if (dirs.existsSync()) {
        for (final line in dirs.readAsLinesSync()) {
          final m = RegExp(r'^XDG_DOWNLOAD_DIR="(.*)"$').firstMatch(line.trim());
          if (m != null) return m.group(1)!.replaceFirst(r'$HOME', home);
        }
      }
    }
    return p.join(home, 'Downloads');
  }
}
