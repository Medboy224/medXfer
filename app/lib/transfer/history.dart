/// The history of transfers, kept by the application (decision J8): the last 100, newest first,
/// in a JSON file of its data folder. Behind [HistoryStore]: the file is one adapter.
library;

import 'dart:convert';
import 'dart:io';

enum Direction { sent, received }

/// How a transfer ended.
enum Outcome { completed, partial, canceled, rejected, failed }

class HistoryEntry {
  const HistoryEntry({
    required this.at,
    required this.direction,
    required this.device,
    required this.files,
    required this.bytes,
    required this.outcome,
    this.folder = '',
    this.name = '',
  });

  factory HistoryEntry.fromJson(Map<String, Object?> m) => HistoryEntry(
    at: DateTime.tryParse('${m['at']}') ?? DateTime.fromMillisecondsSinceEpoch(0),
    direction: m['direction'] == 'received' ? Direction.received : Direction.sent,
    device: (m['device'] as String?) ?? '',
    files: (m['files'] as num?)?.toInt() ?? 0,
    bytes: (m['bytes'] as num?)?.toInt() ?? 0,
    outcome: Outcome.values.firstWhere((o) => o.name == m['outcome'], orElse: () => Outcome.failed),
    folder: (m['folder'] as String?) ?? '',
    name: (m['name'] as String?) ?? '',
  );

  final DateTime at;
  final Direction direction;
  final String device;
  final int files;
  final int bytes;
  final Outcome outcome;

  /// Where the files went: received transfers only ("open the folder").
  final String folder;

  /// The file, or the folder, as the offer named it.
  final String name;

  Map<String, Object?> toJson() => {
    'at': at.toUtc().toIso8601String(),
    'direction': direction.name,
    'device': device,
    'files': files,
    'bytes': bytes,
    'outcome': outcome.name,
    if (folder.isNotEmpty) 'folder': folder,
    if (name.isNotEmpty) 'name': name,
  };
}

abstract interface class HistoryStore {
  Future<List<HistoryEntry>> load();
  Future<void> save(List<HistoryEntry> entries);
}

/// history.json in the application's data folder.
class FileHistoryStore implements HistoryStore {
  FileHistoryStore(this.dir);

  /// The data folder, given once known (the platform tells it asynchronously).
  final Future<String> Function() dir;

  Future<File> _file() async => File('${await dir()}${Platform.pathSeparator}history.json');

  @override
  Future<List<HistoryEntry>> load() async {
    try {
      final f = await _file();
      if (!await f.exists()) return [];
      final list = jsonDecode(await f.readAsString()) as List;
      return [
        for (final m in list)
          if (m is Map<String, Object?>) HistoryEntry.fromJson(m),
      ];
    } on Object {
      return []; // a damaged file is no reason not to start: the history begins again
    }
  }

  @override
  Future<void> save(List<HistoryEntry> entries) async {
    final f = await _file();
    await f.parent.create(recursive: true);
    // Written aside, then renamed: a crash mid-write leaves the previous history whole.
    final tmp = File('${f.path}.tmp');
    await tmp.writeAsString(jsonEncode([for (final e in entries) e.toJson()]), flush: true);
    await tmp.rename(f.path);
  }
}

/// In memory: the tests, and a platform without a data folder.
class MemoryHistoryStore implements HistoryStore {
  List<HistoryEntry> entries = [];

  @override
  Future<List<HistoryEntry>> load() async => List.of(entries);

  @override
  Future<void> save(List<HistoryEntry> e) async => entries = List.of(e);
}
