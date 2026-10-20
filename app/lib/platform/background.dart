/// Running in the background (API-08, WP-B.4): on Android, a foreground service with its
/// notification, without which the app's network is cut within seconds (G2A-05). The desktops
/// need nothing.
library;

import 'dart:io';

import 'folders.dart' show platformChannel;

abstract interface class Background {
  bool get supported;

  /// Starts the service, or updates its notification. progress: 0 to 100, or null.
  Future<void> hold({required String title, required String text, int? progress});

  Future<void> release();

  /// Asks to show notifications (Android 13 on); true once allowed. The service runs either
  /// way: refused, only its notification is hidden.
  Future<bool> askNotifications();
}

class NoBackground implements Background {
  const NoBackground();

  @override
  bool get supported => false;

  @override
  Future<void> hold({required String title, required String text, int? progress}) async {}

  @override
  Future<void> release() async {}

  @override
  Future<bool> askNotifications() async => true;
}

/// MedxferService.kt, through the app's channel.
class AndroidBackground implements Background {
  const AndroidBackground();

  @override
  bool get supported => true;

  @override
  Future<void> hold({required String title, required String text, int? progress}) =>
      platformChannel.invokeMethod<void>('holdService', {'title': title, 'text': text, 'progress': progress ?? -1});

  @override
  Future<void> release() => platformChannel.invokeMethod<void>('releaseService');

  @override
  Future<bool> askNotifications() async => await platformChannel.invokeMethod<bool>('notificationPermission') ?? false;
}

Background systemBackground() => Platform.isAndroid ? const AndroidBackground() : const NoBackground();
