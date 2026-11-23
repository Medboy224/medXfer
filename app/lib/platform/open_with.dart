/// « Ouvrir avec une autre application » (plan J8, WP-8.2): a format medXfer does not play, or
/// a photo to edit, goes to the app the user picks in the system's chooser.
library;

import 'dart:io';

import 'package:flutter/services.dart';

import '../media/media_player.dart';
import 'folders.dart' show platformChannel;
import 'library.dart';

abstract interface class OpenWith {
  bool get supported;

  /// True once the chooser opened.
  Future<bool> open(MediaEntry m);

  /// Shares [m] with another app (the system's share sheet); true once it opened.
  Future<bool> share(MediaEntry m);
}

class NoOpenWith implements OpenWith {
  const NoOpenWith();

  @override
  bool get supported => false;

  @override
  Future<bool> open(MediaEntry m) async => false;

  @override
  Future<bool> share(MediaEntry m) async => false;
}

/// MainActivity.kt: the system's chooser, the file read through its `content://` address.
class AndroidOpenWith implements OpenWith {
  const AndroidOpenWith();

  @override
  bool get supported => true;

  @override
  Future<bool> open(MediaEntry m) async {
    try {
      return await platformChannel.invokeMethod<bool>('openWith', {'uri': contentUriOf(m).toString(), 'mime': mimeOf(m)}) ?? false;
    } on PlatformException {
      return false;
    }
  }

  @override
  Future<bool> share(MediaEntry m) async {
    try {
      return await platformChannel.invokeMethod<bool>('shareMedia', {'uri': contentUriOf(m).toString(), 'mime': mimeOf(m)}) ?? false;
    } on PlatformException {
      return false;
    }
  }
}

OpenWith systemOpenWith() => Platform.isAndroid ? const AndroidOpenWith() : const NoOpenWith();
