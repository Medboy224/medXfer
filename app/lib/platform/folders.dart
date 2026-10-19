/// Where the app keeps its data and where received files go (B6: the core takes its paths
/// from the interface). One adapter, over path_provider.
library;

import 'dart:io';

import 'package:path/path.dart' as p;
import 'package:path_provider/path_provider.dart';

/// The app's folders.
abstract interface class Folders {
  /// Configuration, identity, trusted devices, reports: private to the app.
  Future<String> dataDir();

  /// Where received files go by default.
  Future<String> downloadDir();
}

class SystemFolders implements Folders {
  const SystemFolders();

  @override
  Future<String> dataDir() async => (await getApplicationSupportDirectory()).path;

  /// On the desktop, a medXfer folder in the user's downloads. On Android, until WP-B.4 sets
  /// medXfer/ at the root of the shared storage (STO-15, J2), the app's own external folder.
  @override
  Future<String> downloadDir() async {
    if (Platform.isAndroid) {
      final dir = await getExternalStorageDirectory() ?? await getApplicationDocumentsDirectory();
      return p.join(dir.path, 'medXfer');
    }
    final downloads = await getDownloadsDirectory() ?? await getApplicationDocumentsDirectory();
    return p.join(downloads.path, 'medXfer');
  }
}
