/// The user's settings of the interface (UI-4): theme and accent. Kept in memory for now;
/// saved with the other settings from WP-B.3 on.
library;

import 'package:flutter/material.dart';

import 'theme.dart';

class AppSettings extends ChangeNotifier {
  AppSettings({ThemeChoice? theme, Color? accent})
      : _theme = theme ?? ThemeChoice.system,
        _accent = accent ?? accents.first;

  ThemeChoice _theme;
  Color _accent;

  ThemeChoice get theme => _theme;
  Color get accent => _accent;

  set theme(ThemeChoice value) {
    if (value == _theme) return;
    _theme = value;
    notifyListeners();
  }

  set accent(Color value) {
    if (value == _accent) return;
    _accent = value;
    notifyListeners();
  }
}
