/// What the video player's gestures change (WP-8.2, mockup 26): this window's brightness, given
/// back to the system's as the player closes; and the phone's media volume, the one its own
/// buttons set. Android only (MainActivity.kt); elsewhere nothing, and the gestures do nothing.
library;

import 'dart:io';

import 'package:flutter/services.dart';

import 'folders.dart' show platformChannel;

abstract interface class ScreenControls {
  bool get supported;

  /// From 0 to 1; null when unknown.
  Future<double?> brightness();

  /// Null: the system's brightness again.
  Future<void> setBrightness(double? value);

  /// The media volume, from 0 to 1; null when unknown.
  Future<double?> volume();
  Future<void> setVolume(double value);

  /// The picture drawn under the camera's notch too (true), or kept beside it; null: the
  /// app's own way again.
  Future<void> setUnderNotch(bool? under);
}

class NoScreenControls implements ScreenControls {
  const NoScreenControls();

  @override
  bool get supported => false;
  @override
  Future<double?> brightness() async => null;
  @override
  Future<void> setBrightness(double? value) async {}
  @override
  Future<double?> volume() async => null;
  @override
  Future<void> setVolume(double value) async {}
  @override
  Future<void> setUnderNotch(bool? under) async {}
}

class AndroidScreenControls implements ScreenControls {
  const AndroidScreenControls();

  @override
  bool get supported => true;

  Future<double?> _read(String method) async {
    try {
      return await platformChannel.invokeMethod<double>(method);
    } on PlatformException {
      return null;
    } on MissingPluginException {
      return null; // an older build of the platform side
    }
  }

  Future<void> _write(String method, double? value) async {
    try {
      await platformChannel.invokeMethod<void>(method, {'value': value});
    } on PlatformException {
      // Refused (the volume under « Ne pas déranger »): the video plays on.
    } on MissingPluginException {
      // An older build of the platform side.
    }
  }

  @override
  Future<double?> brightness() => _read('windowBrightness');
  @override
  Future<void> setBrightness(double? value) => _write('setWindowBrightness', value);
  @override
  Future<double?> volume() => _read('mediaVolume');
  @override
  Future<void> setVolume(double value) => _write('setMediaVolume', value);
  @override
  Future<void> setUnderNotch(bool? under) => _write('setUnderNotch', under == null ? null : (under ? 1.0 : 0.0));
}

ScreenControls systemScreenControls() => Platform.isAndroid ? const AndroidScreenControls() : const NoScreenControls();
