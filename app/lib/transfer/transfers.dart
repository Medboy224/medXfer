/// Sending and receiving (WP-B.3 of the J6b plan): the offer that comes in, the transfer under
/// way file by file (pause, resume, skip each one, or the whole), and the history it leaves. It
/// speaks only to the [Core]; the screens read it and call its actions.
library;

import 'dart:async';

import 'package:flutter/foundation.dart';

import '../core/core.dart';
import '../platform/file_picker.dart';
import 'history.dart';

enum ItemStatus { pending, transferring, paused, completed, skipped, failed }

ItemStatus _status(Object? s) => ItemStatus.values.firstWhere((v) => v.name == s, orElse: () => ItemStatus.pending);

/// One file of a transfer.
@immutable
class TransferItem {
  const TransferItem({required this.index, required this.path, required this.size, this.status = ItemStatus.pending, this.bytes = 0});

  /// From the core's queue: {index, rel_path, size, status}.
  factory TransferItem.fromJson(Map<String, Object?> m) => TransferItem(
    index: (m['index'] as num?)?.toInt() ?? 0,
    path: (m['rel_path'] as String?) ?? '',
    size: (m['size'] as num?)?.toInt() ?? 0,
    status: _status(m['status']),
  );

  final int index;
  final String path;
  final int size;
  final ItemStatus status;
  final int bytes;

  double get fraction => status == ItemStatus.completed ? 1 : (size <= 0 ? 0 : (bytes / size).clamp(0, 1).toDouble());

  TransferItem copyWith({ItemStatus? status, int? bytes}) =>
      TransferItem(index: index, path: path, size: size, status: status ?? this.status, bytes: bytes ?? this.bytes);
}

/// The name to show: the folder all the items share, else the core's (which may add "(N files)",
/// in English: the count is shown apart, in the user's language).
String _displayName(Object? core, List<TransferItem> items) {
  if (items.length > 1) {
    String top(String p) => p.replaceAll(r'\', '/').split('/').first;
    final first = top(items.first.path);
    if (first.isNotEmpty && items.every((i) => i.path.contains('/') && top(i.path) == first)) return first;
  }
  return (core as String?) ?? '';
}

List<TransferItem> _items(Object? list) => [
  for (final m in (list as List?) ?? const [])
    if (m is Map<String, Object?>) TransferItem.fromJson(m),
];

/// What another device offers to send (`incoming_offer`), waiting for this user's answer.
@immutable
class IncomingOffer {
  const IncomingOffer({
    required this.device,
    required this.name,
    required this.files,
    required this.bytes,
    this.items = const [],
    this.enoughSpace = true,
    this.freeBytes = -1,
  });

  factory IncomingOffer.fromJson(Map<String, Object?> m) => IncomingOffer(
    device: (m['device_name'] as String?) ?? '',
    name: _displayName(m['file_name'], _items(m['items'])),
    files: (m['total_files'] as num?)?.toInt() ?? 1,
    bytes: (m['file_size'] as num?)?.toInt() ?? 0,
    items: _items(m['items']),
    enoughSpace: m['enough_space'] != false,
    freeBytes: (m['free_bytes'] as num?)?.toInt() ?? -1,
  );

  final String device;
  final String name;
  final int files;
  final int bytes;
  final List<TransferItem> items;

  /// Whether the download folder's disk has room (STO-07): said before accepting.
  final bool enoughSpace;

  /// -1: the system does not say.
  final int freeBytes;
}

enum Stage {
  /// Sent: the other device has not answered yet.
  offered,
  running,
  done,
}

/// The transfer on screen: one at a time, as the core runs one.
@immutable
class ActiveTransfer {
  const ActiveTransfer({
    required this.direction,
    required this.device,
    required this.name,
    required this.totalBytes,
    this.items = const [],
    this.stage = Stage.offered,
    this.bytes = 0,
    this.speedMBps = 0,
    this.etaSeconds = 0,
    this.paused = false,
    this.outcome,
    this.started,
  });

  final Direction direction;
  final String device;
  final String name;
  final int totalBytes;
  final List<TransferItem> items;
  final Stage stage;
  final int bytes;
  final double speedMBps;
  final int etaSeconds;
  final bool paused;
  final Outcome? outcome;
  final DateTime? started;

  double get fraction => outcome == Outcome.completed ? 1 : (totalBytes <= 0 ? 0 : (bytes / totalBytes).clamp(0, 1).toDouble());

  int count(ItemStatus s) => items.where((i) => i.status == s).length;

  ActiveTransfer copyWith({
    List<TransferItem>? items,
    Stage? stage,
    int? bytes,
    int? totalBytes,
    double? speedMBps,
    int? etaSeconds,
    bool? paused,
    Outcome? outcome,
    DateTime? started,
  }) => ActiveTransfer(
    direction: direction,
    device: device,
    name: name,
    totalBytes: totalBytes ?? this.totalBytes,
    items: items ?? this.items,
    stage: stage ?? this.stage,
    bytes: bytes ?? this.bytes,
    speedMBps: speedMBps ?? this.speedMBps,
    etaSeconds: etaSeconds ?? this.etaSeconds,
    paused: paused ?? this.paused,
    outcome: outcome ?? this.outcome,
    started: started ?? this.started,
  );
}

/// Something to tell the user once.
sealed class TransferNotice {
  const TransferNotice();
}

/// The core refused to send, or to receive (no session, unreadable file, no room…).
class TransferFailed extends TransferNotice {
  const TransferFailed(this.message, {this.noSpace = false});
  final String message;
  final bool noSpace;
}

/// The disk filled up: the receiver paused itself, keeping what it got (STO-07).
class DiskFull extends TransferNotice {
  const DiskFull(this.folder);
  final String folder;
}

/// One file could not be sent or written; the others go on.
class FileError extends TransferNotice {
  const FileError(this.file, this.error);
  final String file;
  final String error;
}

class Transfers extends ChangeNotifier {
  Transfers(this.core, {required this.history, required this.peerName, required this.downloadDir, this.placeFor}) {
    _sub = core.events.listen(take);
  }

  final Core core;
  final HistoryStore history;

  /// The device of the session: the sender's side names it so.
  final String Function() peerName;

  /// Where received files go (the core's download folder): the history keeps it.
  final Future<String> Function() downloadDir;

  /// Where an accepted offer goes, when the platform chooses (Android: `medXfer/<type>/`,
  /// decision J2); null: the core's download folder.
  final Future<String?> Function(IncomingOffer offer)? placeFor;

  /// The folder of the transfer being received, for the history.
  String? _receiveDir;

  late final StreamSubscription<CoreEvent> _sub;
  final _notices = StreamController<TransferNotice>.broadcast();
  final _mine = <String>{};

  IncomingOffer? _offer;
  ActiveTransfer? _active;
  List<HistoryEntry> _history = const [];

  /// How many transfers the history keeps.
  static const historySize = 100;

  IncomingOffer? get offer => _offer;
  ActiveTransfer? get active => _active;
  List<HistoryEntry> get entries => _history;
  Stream<TransferNotice> get notices => _notices.stream;

  Future<void> loadHistory() async {
    _history = await history.load();
    notifyListeners();
  }

  // Actions.

  /// Sends files and folders, by path, to the device of the session.
  Future<void> send(List<String> paths) async {
    if (paths.isEmpty) return;
    await _call('send', {'paths': paths});
  }

  /// Sends what the user picked: paths (desktops), or files open by descriptor (Android,
  /// STO-16: the core owns them from the call on, and closes them).
  Future<void> sendPicked(Picked picked) async {
    if (picked.paths.isNotEmpty) return send(picked.paths);
    if (picked.files.isEmpty) return;
    await _call('send', {
      'items': [for (final f in picked.files) f.toJson()],
    });
  }

  Future<void> accept() async {
    final offer = _offer;
    if (offer == null) return;
    final dir = await placeFor?.call(offer);
    _receiveDir = dir;
    final reply = await _call('respond_offer', {'accept': true, if (dir != null && dir.isNotEmpty) 'save_dir': dir});
    // Refused for lack of room, the offer stays: the user may free some and accept again.
    if (reply.ok) {
      _offer = null;
      _active = ActiveTransfer(
        direction: Direction.received,
        device: offer.device,
        name: offer.name,
        totalBytes: offer.bytes,
        items: offer.items,
        stage: Stage.running,
        started: DateTime.now(),
      );
      notifyListeners();
    }
  }

  Future<void> reject() async {
    final offer = _offer;
    if (offer == null) return;
    _offer = null;
    notifyListeners();
    await _call('respond_offer', {'accept': false});
    await _remember(
      HistoryEntry(
        at: DateTime.now(),
        direction: Direction.received,
        device: offer.device,
        files: offer.files,
        bytes: offer.bytes,
        outcome: Outcome.rejected,
        name: offer.name,
      ),
    );
  }

  Future<void> pause() => _call('pause');
  Future<void> resume() => _call('resume');
  Future<void> cancel() => _call('cancel');
  Future<void> skip(int index) => _call('skip_file', {'item_index': index});
  Future<void> pauseFile(int index) => _call('pause_file', {'item_index': index});
  Future<void> resumeFile(int index) => _call('resume_file', {'item_index': index});

  /// The finished transfer leaves the screen.
  void dismiss() {
    if (_active?.stage != Stage.done) return;
    _active = null;
    notifyListeners();
  }

  Future<CoreReply> _call(String action, [Map<String, Object?>? payload]) async {
    final reply = await core.call(action, payload);
    if (reply.id != null) _mine.add(reply.id!);
    for (final e in reply.events) {
      take(e);
    }
    return reply;
  }

  // Events.

  @visibleForTesting
  void take(CoreEvent e) {
    final m = e.map;
    final a = _active;
    switch (e.name) {
      case 'file_offered':
        _active = ActiveTransfer(
          direction: Direction.sent,
          device: peerName(),
          name: (m['file_name'] as String?) ?? '',
          totalBytes: (m['file_size'] as num?)?.toInt() ?? 0,
          items: [TransferItem(index: 0, path: (m['file_name'] as String?) ?? '', size: (m['file_size'] as num?)?.toInt() ?? 0)],
        );
      case 'batch_offered':
        _active = ActiveTransfer(
          direction: Direction.sent,
          device: peerName(),
          name: _displayName(m['summary'], _items(m['items'])),
          totalBytes: (m['total_bytes'] as num?)?.toInt() ?? 0,
          items: _items(m['items']),
        );
      case 'transfer_accepted':
        if (a == null) return;
        _active = a.copyWith(stage: Stage.running, started: DateTime.now());
      case 'incoming_offer':
        _offer = IncomingOffer.fromJson(m);
      case 'transfer_start':
        if (a == null || a.stage == Stage.done) return;
        _active = a.copyWith(stage: Stage.running, totalBytes: (m['batch_total_bytes'] as num?)?.toInt());
      case 'batch_item_transferring' || 'item_completed' || 'item_failed' || 'file_skipped' || 'file_paused' || 'file_resumed':
        if (a == null) return;
        _active = a.copyWith(items: _merge(a.items, _items(m['items'])));
        if (e.name == 'item_failed') _notices.add(FileError(_pathOf(a, m['item_index']), (m['error'] as String?) ?? ''));
      case 'batch_paused_waiting':
        if (a == null) return;
        _active = a.copyWith(items: _merge(a.items, _items(m['items'])), speedMBps: 0);
      case 'transfer_progress':
        if (a == null) return;
        final progress = {
          for (final p in (m['items'] as List?) ?? const [])
            if (p is Map<String, Object?>) (p['index'] as num).toInt(): (p['bytes'] as num?)?.toInt() ?? 0,
        };
        _active = a.copyWith(
          stage: Stage.running,
          bytes: (m['batch_bytes'] as num?)?.toInt(),
          totalBytes: (m['batch_total_bytes'] as num?)?.toInt(),
          speedMBps: (m['speed_mbps'] as num?)?.toDouble(),
          etaSeconds: (m['eta_seconds'] as num?)?.toInt(),
          paused: m['is_paused'] == true,
          items: [
            for (final i in a.items)
              progress.containsKey(i.index) ? i.copyWith(bytes: progress[i.index], status: i.status == ItemStatus.pending ? ItemStatus.transferring : null) : i,
          ],
        );
      case 'transfer_paused':
        if (a == null) return;
        _active = a.copyWith(paused: true, speedMBps: 0);
      case 'transfer_resumed':
        if (a == null) return;
        _active = a.copyWith(paused: false);
      case 'disk_full':
        _notices.add(DiskFull((m['save_dir'] as String?) ?? ''));
      case 'transfer_error':
        // Each file's failure comes again as item_failed; a transfer without items says it here.
        if (a == null || a.items.length > 1) return;
        _notices.add(FileError((m['file'] as String?) ?? '', (m['error'] as String?) ?? ''));
      case 'transfer_rejected':
        if (a == null || a.direction != Direction.sent || a.stage != Stage.offered) return;
        _finish(a, Outcome.rejected);
      case 'transfer_canceled':
        if (a == null || a.stage == Stage.done) return;
        _finish(a, Outcome.canceled);
      case 'transfer_complete':
        if (a == null || a.stage == Stage.done) return;
        final items = _merge(a.items, _items(m['items']));
        final whole = items.every((i) => i.status == ItemStatus.completed);
        _finish(a.copyWith(items: items, bytes: whole ? a.totalBytes : null), whole ? Outcome.completed : Outcome.partial);
      case 'disconnected':
        _offer = null;
        if (a == null || a.stage == Stage.done) break;
        _finish(a, Outcome.failed);
      case 'action_error':
        if (e.id == null || !_mine.contains(e.id)) return;
        _notices.add(TransferFailed((m['error'] as String?) ?? '', noSpace: m['code'] == 'NO_SPACE'));
        // A send refused before any offer: nothing on screen to wait for.
        if (a != null && a.stage == Stage.offered && a.direction == Direction.sent) _active = null;
      default:
        return;
    }
    notifyListeners();
  }

  /// The core's queue gives the statuses; the bytes seen so far stay.
  static List<TransferItem> _merge(List<TransferItem> old, List<TransferItem> fresh) {
    if (fresh.isEmpty) return old;
    final bytes = {for (final i in old) i.index: i.bytes};
    return [for (final i in fresh) i.copyWith(bytes: i.status == ItemStatus.completed ? i.size : bytes[i.index] ?? 0)];
  }

  static String _pathOf(ActiveTransfer a, Object? index) {
    final i = (index as num?)?.toInt();
    return a.items.where((x) => x.index == i).map((x) => x.path).firstOrNull ?? '';
  }

  void _finish(ActiveTransfer a, Outcome outcome) {
    _active = a.copyWith(stage: Stage.done, outcome: outcome, speedMBps: 0, etaSeconds: 0, paused: false);
    unawaited(_record(a, outcome));
  }

  Future<void> _record(ActiveTransfer a, Outcome outcome) async {
    final folder = a.direction == Direction.received ? (_receiveDir ?? await downloadDir()) : '';
    await _remember(
      HistoryEntry(
        at: DateTime.now(),
        direction: a.direction,
        device: a.device,
        files: a.items.isEmpty ? 1 : a.items.length,
        bytes: a.totalBytes,
        outcome: outcome,
        folder: folder,
        name: a.name,
      ),
    );
  }

  Future<void> _remember(HistoryEntry entry) async {
    _history = [entry, ..._history].take(historySize).toList();
    notifyListeners();
    await history.save(_history);
  }

  @override
  void dispose() {
    _sub.cancel();
    _notices.close();
    super.dispose();
  }
}
