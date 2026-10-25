/// Where received files go (WP-B.10, decisions J2 and J16): the folder the user chose in the
/// settings, else the default one. On Android, the default is medXfer/ at the root of the
/// storage (Download/medXfer/ without the access to all files), and the files may be sorted by
/// type; a folder of the user's choice needs that access. On the desktops, the default is
/// Downloads/medXfer.
library;

import 'dart:io';
import 'dart:math';

import '../platform/folders.dart';
import '../platform/storage.dart';
import '../settings.dart';
import 'transfers.dart';

/// The folder of one offer, and the chosen folder it replaces when that one is gone.
class ReceivePlace {
  const ReceivePlace(this.dir, {this.gone = ''});

  final String dir;

  /// The folder chosen in the settings, no longer there or no longer allowed: [dir] is the
  /// default one instead, and the user is told.
  final String gone;
}

class ReceiveFolders {
  ReceiveFolders({required this.settings, required this.storage, required this.folders, Future<bool> Function(String dir)? exists})
    : _exists = exists ?? ((dir) => Directory(dir).exists());

  final AppSettings settings;
  final ReceiveStorage storage;
  final Folders folders;
  final Future<bool> Function(String dir) _exists;

  /// Android: received files may be sorted by type.
  bool get sorts => storage.supported;

  /// Where files go when the user chose no folder.
  Future<String> defaultDir() async => storage.supported ? storage.root() : folders.downloadDir();

  /// Whether the user may choose a folder: always on the desktops; on Android, with the access
  /// to all files (else only Download/medXfer/ is writable).
  Future<bool> canChoose() async => !storage.supported || await storage.allFiles();

  /// The folder received files go to now, for the settings: the chosen one, else the default.
  Future<String> current() async => (await placeFor(null)).dir;

  /// The folder of offer: the chosen one if it is still there and allowed, else the default;
  /// on Android, in the offer's type folder when the user sorts by type.
  Future<ReceivePlace> placeFor(IncomingOffer? offer) async {
    final chosen = settings.receiveDir;
    var dir = await defaultDir();
    var gone = '';
    if (chosen.isNotEmpty) {
      if (await canChoose() && await _exists(chosen)) {
        dir = chosen;
      } else {
        gone = chosen;
      }
    }
    if (offer != null && sorts && settings.sortByType) {
      dir = '$dir/${typeFolderFor(offer.items.isEmpty ? [offer.name] : [for (final i in offer.items) i.path])}';
    }
    return ReceivePlace(dir, gone: gone);
  }

  /// Whether medXfer can write in dir: a file made, then removed.
  Future<bool> writable(String dir) async {
    final probe = File('$dir${Platform.pathSeparator}.medxfer-${Random().nextInt(1 << 32).toRadixString(16)}');
    try {
      await probe.writeAsString('');
      await probe.delete();
      return true;
    } on FileSystemException {
      return false;
    }
  }
}
