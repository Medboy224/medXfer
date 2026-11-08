/// Keeps the device awake while a transfer runs (#256): a phone whose screen goes off may lose
/// the network that carries it, the one it joined or the one it created (real tests of 2 and 3
/// November 2026), and a computer that sleeps stops it. Android keeps the screen on, Windows
/// keeps the computer from sleeping; both let go at the end of the transfer.
library;

import 'dart:io';

import 'package:flutter/services.dart';

import 'folders.dart' show platformChannel;

abstract interface class Awake {
  Future<void> keep(bool on);
}

/// Android (MainActivity.kt) and Windows (flutter_window.cpp), by a channel.
class ChannelAwake implements Awake {
  const ChannelAwake(this.channel);

  final MethodChannel channel;

  @override
  Future<void> keep(bool on) async {
    try {
      await channel.invokeMethod<void>('keepAwake', {'on': on});
    } on MissingPluginException {
      // An older build of the platform side: the transfer runs all the same.
    }
  }
}

class NoAwake implements Awake {
  const NoAwake();

  @override
  Future<void> keep(bool on) async {}
}

Awake systemAwake() => Platform.isAndroid
    ? const ChannelAwake(platformChannel)
    : Platform.isWindows
        ? const ChannelAwake(MethodChannel('dev.medxfer.app/power'))
        : const NoAwake();
