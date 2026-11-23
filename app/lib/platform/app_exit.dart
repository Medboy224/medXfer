/// Leaving the app by the back button on the home screen (real test of 22 November 2026): to
/// the background, all that runs kept; or quit, all stopped, even what ran in the background
/// (the core runs in the app's process and kept a session open once the screen had gone).
library;

import 'dart:io';

import 'package:flutter/services.dart';

import 'folders.dart' show platformChannel;

abstract interface class AppExit {
  /// The app goes to the background, as the home button does: all it runs goes on.
  Future<void> toBack();

  /// The app's task and process end. Called once all was stopped.
  Future<void> quit();
}

/// MainActivity.kt, through the app's channel.
class AndroidAppExit implements AppExit {
  const AndroidAppExit();

  @override
  Future<void> toBack() => platformChannel.invokeMethod<void>('toBack');

  @override
  Future<void> quit() => platformChannel.invokeMethod<void>('quit');
}

/// Elsewhere: the platform's own way out.
class SystemExit implements AppExit {
  const SystemExit();

  @override
  Future<void> toBack() => SystemNavigator.pop();

  @override
  Future<void> quit() => SystemNavigator.pop();
}

/// How the app leaves; a test puts its own.
AppExit appExit = Platform.isAndroid ? const AndroidAppExit() : const SystemExit();
