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

  /// The same for any file, by its path (a PDF, a media the system's library does not know).
  Future<bool> openFile(String path, String mime);
  Future<bool> shareFile(String path, String mime);
}

class NoOpenWith implements OpenWith {
  const NoOpenWith();

  @override
  bool get supported => false;

  @override
  Future<bool> open(MediaEntry m) async => false;

  @override
  Future<bool> share(MediaEntry m) async => false;

  @override
  Future<bool> openFile(String path, String mime) async => false;

  @override
  Future<bool> shareFile(String path, String mime) async => false;
}

/// MainActivity.kt: the system's chooser, the file read through its `content://` address.
class AndroidOpenWith implements OpenWith {
  const AndroidOpenWith();

  @override
  bool get supported => true;

  @override
  Future<bool> open(MediaEntry m) async {
    // Not known to the system's library (opened from the Files tab): by its path.
    if (m.id < 0) return openFile(m.path, mimeOf(m));
    try {
      return await platformChannel.invokeMethod<bool>('openWith', {'uri': contentUriOf(m).toString(), 'mime': mimeOf(m)}) ?? false;
    } on PlatformException {
      return false;
    }
  }

  @override
  Future<bool> share(MediaEntry m) async {
    if (m.id < 0) return shareFile(m.path, mimeOf(m));
    try {
      return await platformChannel.invokeMethod<bool>('shareMedia', {'uri': contentUriOf(m).toString(), 'mime': mimeOf(m)}) ?? false;
    } on PlatformException {
      return false;
    }
  }

  @override
  Future<bool> openFile(String path, String mime) => _byPath('openWith', path, mime);

  @override
  Future<bool> shareFile(String path, String mime) => _byPath('shareMedia', path, mime);

  /// The file's content:// address from MediaStore, which knows the shared storage's files.
  Future<bool> _byPath(String method, String path, String mime) async {
    try {
      final uri = await platformChannel.invokeMethod<String>('libContentUri', {'path': path});
      if (uri == null) return false;
      return await platformChannel.invokeMethod<bool>(method, {'uri': uri, 'mime': mime}) ?? false;
    } on PlatformException {
      return false;
    }
  }
}

/// Windows and Linux (WP-8.2b): « Ouvrir avec… », the file given as one argument, never a command
/// built from text. Windows asks which app (OpenWith.exe, its own « Ouvrir avec » window; through
/// rundll32, a path with spaces came quoted and nothing opened, real test of 25 November 2026);
/// Linux opens it in the app the desktop gives its kind. No sharing sheet on a computer.
class DesktopOpenWith implements OpenWith {
  const DesktopOpenWith();

  @override
  bool get supported => true;

  @override
  Future<bool> open(MediaEntry m) => openFile(m.path, mimeOf(m));

  @override
  Future<bool> share(MediaEntry m) async => false;

  @override
  Future<bool> openFile(String path, String mime) async {
    if (path.isEmpty) return false;
    try {
      if (Platform.isWindows) {
        final windows = Platform.environment['SystemRoot'] ?? r'C:\Windows';
        final chooser = '$windows\\System32\\OpenWith.exe';
        // No chooser (an old Windows): its app for that kind, as a double click would.
        await Process.start(File(chooser).existsSync() ? chooser : 'explorer.exe', [path], mode: ProcessStartMode.detached);
      } else {
        await Process.start(Platform.isMacOS ? 'open' : 'xdg-open', [path], mode: ProcessStartMode.detached);
      }
      return true;
    } on Object {
      return false;
    }
  }

  @override
  Future<bool> shareFile(String path, String mime) async => false;
}

OpenWith systemOpenWith() => Platform.isAndroid
    ? const AndroidOpenWith()
    : Platform.isWindows || Platform.isLinux || Platform.isMacOS
    ? const DesktopOpenWith()
    : const NoOpenWith();
