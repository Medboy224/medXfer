/// The user's settings of the interface: theme and accent (UI-4), and the developer mode
/// (DEV-13). Saved in settings.json in the application's data folder, behind [SettingsStore].
library;

import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';

import 'theme.dart';

abstract interface class SettingsStore {
  Future<Map<String, Object?>> load();
  Future<void> save(Map<String, Object?> values);
}

/// settings.json in the application's data folder.
class FileSettingsStore implements SettingsStore {
  FileSettingsStore(this.dir);

  /// The data folder, given once known (the platform tells it asynchronously).
  final Future<String> Function() dir;

  Future<File> _file() async => File('${await dir()}${Platform.pathSeparator}settings.json');

  @override
  Future<Map<String, Object?>> load() async {
    try {
      final f = await _file();
      if (!await f.exists()) return {};
      final m = jsonDecode(await f.readAsString());
      return m is Map<String, Object?> ? m : {};
    } on Object {
      return {}; // a damaged file: the defaults
    }
  }

  @override
  Future<void> save(Map<String, Object?> values) async {
    final f = await _file();
    await f.parent.create(recursive: true);
    // Written aside, then renamed: a crash mid-write leaves the previous settings whole.
    final tmp = File('${f.path}.tmp');
    await tmp.writeAsString(jsonEncode(values), flush: true);
    await tmp.rename(f.path);
  }
}

/// In memory: the tests.
class MemorySettingsStore implements SettingsStore {
  Map<String, Object?> values = {};

  @override
  Future<Map<String, Object?>> load() async => Map.of(values);

  @override
  Future<void> save(Map<String, Object?> v) async => values = Map.of(v);
}

class AppSettings extends ChangeNotifier {
  AppSettings({ThemeChoice? theme, Color? accent, this._devMode = false, this._store})
      : _theme = theme ?? ThemeChoice.system,
        _accent = accent ?? accents.first;

  final SettingsStore? _store;
  ThemeChoice _theme;
  Color _accent;
  bool _devMode;

  ThemeChoice get theme => _theme;
  Color get accent => _accent;

  /// The developer mode: off by default, it shows a banner and the developer panel (DEV-13).
  bool get devMode => _devMode;

  /// Reads the saved settings; what is missing or unknown keeps its default.
  Future<void> load() async {
    final m = await _store?.load() ?? const {};
    _theme = ThemeChoice.values.firstWhere((t) => t.name == m['theme'], orElse: () => _theme);
    final i = m['accent'];
    if (i is int && i >= 0 && i < accents.length) _accent = accents[i];
    if (m['dev_mode'] is bool) _devMode = m['dev_mode'] as bool;
    notifyListeners();
  }

  set theme(ThemeChoice value) {
    if (value == _theme) return;
    _theme = value;
    _changed();
  }

  set accent(Color value) {
    if (value == _accent) return;
    _accent = value;
    _changed();
  }

  set devMode(bool value) {
    if (value == _devMode) return;
    _devMode = value;
    _changed();
  }

  /// The saves, one after the other: two at once would write the same file.
  Future<void> _saving = Future.value();

  /// Done once every change so far is saved.
  Future<void> get saved => _saving;

  void _changed() {
    notifyListeners();
    final store = _store;
    if (store == null) return;
    final values = {'theme': _theme.name, 'accent': accents.indexOf(_accent), 'dev_mode': _devMode};
    // Not saved: still applied until the app closes.
    _saving = _saving.then((_) => store.save(values)).catchError((Object _) {});
  }
}
