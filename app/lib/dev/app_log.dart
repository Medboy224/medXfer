import 'dart:collection';
import 'dart:io';

import 'package:flutter/foundation.dart';

/// The application's own log: what the core does not see (its Bluetooth, its networks), for the
/// diagnostic bundle (DEV-11), where the core masks it as the rest. Kept in memory only, the
/// newest [maxLines] lines; release builds too, where nothing else keeps it (a Windows app has
/// no console).
class AppLog {
  AppLog({this.maxLines = 2000});

  /// The one [install] fills.
  static final instance = AppLog();

  final int maxLines;
  final _lines = ListQueue<String>();

  /// Adds a line, with the time (UTC).
  void add(String line) {
    _lines.add('${DateTime.now().toUtc().toIso8601String()} $line');
    while (_lines.length > maxLines) {
      _lines.removeFirst();
    }
  }

  /// The lines kept, oldest first.
  String get text => _lines.isEmpty ? '' : '${_lines.join('\n')}\n';

  /// Every debugPrint goes to [instance] too, but the core's own log lines, which its journal
  /// keeps already. The first line says the system.
  static void install() {
    instance.add('app: ${Platform.operatingSystem} ${Platform.operatingSystemVersion}');
    final print = debugPrint;
    debugPrint = (String? message, {int? wrapWidth}) {
      if (message != null && !message.startsWith('core log: ')) instance.add(message);
      print(message, wrapWidth: wrapWidth);
    };
  }
}
