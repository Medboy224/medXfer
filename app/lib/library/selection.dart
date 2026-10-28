/// What the user picked in the explorer (WP-B.9): it survives a change of tab, and the basket
/// shows its count and size before sending (§1.2). Sending follows the core's rule, paths or
/// descriptors, never both:
///   - without folders, every file is opened by the app and handed by descriptor, each under its
///     own name (an app's base.apk goes as "<label>.apk");
///   - with folders, everything goes by path, the core walking the folders;
///   - apps and folders together: sent apart ([Mixed]).
/// On a desktop, everything goes by path: its files and folders together (desktop_picks.dart).
library;

import 'package:flutter/foundation.dart';

enum PickKind { app, media, file, folder }

@immutable
class Pick {
  const Pick({required this.path, required this.name, required this.size, required this.kind});

  final String path;

  /// What the other device sees.
  final String name;

  /// 0 for a folder: its size is known once the core walks it.
  final int size;
  final PickKind kind;
}

/// How a selection goes to the core.
sealed class SendPlan {
  const SendPlan();
}

class ByPath extends SendPlan {
  const ByPath(this.paths);
  final List<String> paths;
}

class ByDescriptor extends SendPlan {
  const ByDescriptor(this.files);
  final List<({String path, String name})> files;
}

/// Apps and folders together: the core takes paths or descriptors, not both.
class Mixed extends SendPlan {
  const Mixed();
}

class Selection extends ChangeNotifier {
  final _items = <String, Pick>{};

  Iterable<Pick> get items => _items.values;
  int get count => _items.length;
  bool get isEmpty => _items.isEmpty;
  int get bytes => _items.values.fold(0, (sum, p) => sum + p.size);
  bool contains(String path) => _items.containsKey(path);

  void toggle(Pick p) {
    if (_items.remove(p.path) == null) _items[p.path] = p;
    notifyListeners();
  }

  /// Picks them, those already picked staying so (a desktop's pickers, a drop).
  void addAll(Iterable<Pick> picks) {
    var added = false;
    for (final p in picks) {
      added = _items.putIfAbsent(p.path, () => p) == p || added;
    }
    if (added) notifyListeners();
  }

  /// All of them picked, or none if all already were (the "select all" circle).
  void toggleAll(Iterable<Pick> all) {
    final list = all.toList();
    if (list.isEmpty) return;
    if (list.every((p) => _items.containsKey(p.path))) {
      for (final p in list) {
        _items.remove(p.path);
      }
    } else {
      for (final p in list) {
        _items[p.path] = p;
      }
    }
    notifyListeners();
  }

  /// A folder's size, known once walked.
  void setSize(String path, int size) {
    final p = _items[path];
    if (p == null || p.size == size) return;
    _items[path] = Pick(path: p.path, name: p.name, size: size, kind: p.kind);
    notifyListeners();
  }

  bool allOf(Iterable<Pick> all) => all.isNotEmpty && all.every((p) => _items.containsKey(p.path));

  void clear() {
    if (_items.isEmpty) return;
    _items.clear();
    notifyListeners();
  }

  /// [byPath]: the platform sends paths only (desktops).
  SendPlan plan({bool byPath = false}) {
    if (byPath) return ByPath([for (final p in _items.values) p.path]);
    final folders = _items.values.any((p) => p.kind == PickKind.folder);
    final apps = _items.values.any((p) => p.kind == PickKind.app);
    if (folders && apps) return const Mixed();
    if (folders) return ByPath([for (final p in _items.values) p.path]);
    return ByDescriptor([for (final p in _items.values) (path: p.path, name: p.name)]);
  }
}
