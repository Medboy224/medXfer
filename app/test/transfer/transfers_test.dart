// The transfers model on the fake core: sending, receiving, each file's controls, and the
// history they leave (WP-B.3).
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';
import 'package:medxfer/platform/file_picker.dart';
import 'package:medxfer/transfer/history.dart';
import 'package:medxfer/transfer/receive_folders.dart';
import 'package:medxfer/transfer/transfers.dart';

Future<void> _settle() => Future<void>.delayed(Duration.zero);

Map<String, Object?> _q(int i, String path, int size, String status) => {'index': i, 'rel_path': path, 'size': size, 'status': status};

(FakeCore, Transfers, MemoryHistoryStore) _model() {
  final core = FakeCore();
  final store = MemoryHistoryStore();
  final t = Transfers(core, history: store, peerName: () => 'PC du salon', downloadDir: () async => '/home/a/medXfer');
  return (core, t, store);
}

void main() {
  test('sending: offered, accepted, file by file, complete; the history keeps it', () async {
    final (core, t, store) = _model();
    core.on('send', (_) => FakeAnswer(broadcast: [
          CoreEvent('batch_offered', data: {
            'summary': 'Vacances',
            'total_files': 2,
            'total_bytes': 300,
            'items': [_q(0, 'Vacances/a.jpg', 100, 'pending'), _q(1, 'Vacances/b.jpg', 200, 'pending')],
          }),
        ]));
    await t.send(['/home/a/Vacances']);
    expect(core.calls.last.payload, {
      'paths': ['/home/a/Vacances'],
    });
    await _settle();
    var a = t.active!;
    expect((a.direction, a.device, a.name, a.stage, a.items.length), (Direction.sent, 'PC du salon', 'Vacances', Stage.offered, 2));

    t.take(const CoreEvent('transfer_accepted', data: {}));
    expect(t.active!.stage, Stage.running);
    t.take(const CoreEvent('transfer_progress', data: {
      'batch_bytes': 150,
      'batch_total_bytes': 300,
      'speed_mbps': 12.5,
      'eta_seconds': 3,
      'is_paused': false,
      'items': [
        {'index': 0, 'bytes': 100, 'size': 100},
        {'index': 1, 'bytes': 50, 'size': 200},
      ],
    }));
    a = t.active!;
    expect(a.fraction, 0.5);
    expect(a.speedMBps, 12.5);
    expect(a.items[1].bytes, 50);
    expect(a.items[1].status, ItemStatus.transferring);

    t.take(CoreEvent('item_completed', data: {
      'item_index': 0,
      'items': [_q(0, 'Vacances/a.jpg', 100, 'completed'), _q(1, 'Vacances/b.jpg', 200, 'transferring')],
    }));
    expect(t.active!.items[0].status, ItemStatus.completed);
    expect(t.active!.items[1].bytes, 50, reason: 'the bytes seen stay when the queue comes again');

    t.take(CoreEvent('transfer_complete', data: {
      'message': 'Receiver confirmed transfer complete',
      'items': [_q(0, 'Vacances/a.jpg', 100, 'completed'), _q(1, 'Vacances/b.jpg', 200, 'completed')],
    }));
    a = t.active!;
    expect((a.stage, a.outcome, a.fraction), (Stage.done, Outcome.completed, 1.0));
    await _settle();
    final h = store.entries.single;
    expect((h.direction, h.device, h.files, h.bytes, h.outcome, h.name, h.folder), (Direction.sent, 'PC du salon', 2, 300, Outcome.completed, 'Vacances', ''));
    t.dismiss();
    expect(t.active, isNull);
  });

  test('a skipped file: done, but partial', () async {
    final (_, t, store) = _model();
    t.take(CoreEvent('batch_offered', data: {
      'summary': 'x',
      'total_files': 2,
      'total_bytes': 2,
      'items': [_q(0, 'a', 1, 'pending'), _q(1, 'b', 1, 'pending')],
    }));
    t.take(const CoreEvent('transfer_accepted', data: {}));
    t.take(CoreEvent('transfer_complete', data: {
      'items': [_q(0, 'a', 1, 'completed'), _q(1, 'b', 1, 'skipped')],
    }));
    expect(t.active!.outcome, Outcome.partial);
    await _settle();
    expect(store.entries.single.outcome, Outcome.partial);
  });

  test('receiving: the offer, accepted, then the folder in the history', () async {
    final (core, t, store) = _model();
    core.on('respond_offer', (_) => const FakeAnswer());
    t.take(CoreEvent('incoming_offer', data: {
      'device_name': 'Galaxy A57',
      // The core's name says the count in English: the folder's own name is shown instead.
      'file_name': 'Photos (3 files)',
      'file_size': 3000,
      'total_files': 3,
      'is_batch': true,
      'items': [_q(0, 'Photos/1.jpg', 1000, 'pending'), _q(1, 'Photos/2.jpg', 1000, 'pending'), _q(2, 'Photos/3.jpg', 1000, 'pending')],
      'free_bytes': 1 << 30,
      'enough_space': true,
    }));
    final o = t.offer!;
    expect((o.device, o.name, o.files, o.bytes, o.items.length, o.enoughSpace), ('Galaxy A57', 'Photos', 3, 3000, 3, true));

    await t.accept();
    expect(core.calls.last.payload, {'accept': true});
    expect(t.offer, isNull);
    expect((t.active!.direction, t.active!.stage, t.active!.device), (Direction.received, Stage.running, 'Galaxy A57'));

    t.take(CoreEvent('transfer_complete', data: {
      'message': 'done',
      'items': [for (var i = 0; i < 3; i++) _q(i, 'Photos/${i + 1}.jpg', 1000, 'completed')],
    }));
    await _settle();
    await _settle();
    final h = store.entries.single;
    expect((h.direction, h.outcome, h.folder, h.files), (Direction.received, Outcome.completed, '/home/a/medXfer', 3));
  });

  test('no room: refused by the core, the offer stays, the user is told', () async {
    final (core, t, _) = _model();
    core.on(
      'respond_offer',
      (_) => const FakeAnswer(ok: false, error: 'no room', replies: [
        CoreEvent('action_error', data: {'code': 'NO_SPACE', 'error': 'cannot receive into /x: not enough space'}),
      ]),
    );
    final notices = <TransferNotice>[];
    t.notices.listen(notices.add);
    t.take(const CoreEvent('incoming_offer', data: {'device_name': 'A', 'file_name': 'big.iso', 'file_size': 9, 'total_files': 1, 'enough_space': false, 'free_bytes': 1}));
    expect(t.offer!.enoughSpace, isFalse);
    await t.accept();
    await _settle();
    expect(t.offer, isNotNull);
    expect(t.active, isNull);
    expect((notices.single as TransferFailed).noSpace, isTrue);
  });

  test('declined here: answered no, and kept in the history', () async {
    final (core, t, store) = _model();
    core.on('respond_offer', (_) => const FakeAnswer());
    t.take(const CoreEvent('incoming_offer', data: {'device_name': 'A', 'file_name': 'f.txt', 'file_size': 5, 'total_files': 1}));
    await t.reject();
    expect(core.calls.last.payload, {'accept': false});
    expect(t.offer, isNull);
    expect(store.entries.single.outcome, Outcome.rejected);
  });

  test('declined there, canceled, or the link lost: each ends the transfer as it is', () async {
    final (_, t, store) = _model();
    t.take(const CoreEvent('file_offered', data: {'file_name': 'a.txt', 'file_size': 4}));
    t.take(const CoreEvent('transfer_rejected', data: {'message': 'Transfer rejected by user'}));
    expect(t.active!.outcome, Outcome.rejected);
    t.dismiss();

    t.take(const CoreEvent('file_offered', data: {'file_name': 'b.txt', 'file_size': 4}));
    t.take(const CoreEvent('transfer_accepted', data: {}));
    t.take(const CoreEvent('transfer_canceled', data: {'message': 'Transfer canceled by peer'}));
    expect(t.active!.outcome, Outcome.canceled);
    t.dismiss();

    t.take(const CoreEvent('file_offered', data: {'file_name': 'c.txt', 'file_size': 4}));
    t.take(const CoreEvent('transfer_accepted', data: {}));
    t.take(const CoreEvent('disconnected', data: {'reason': 'connection timed out'}));
    expect(t.active!.outcome, Outcome.failed);
    await _settle();
    expect(store.entries.map((e) => e.outcome), [Outcome.failed, Outcome.canceled, Outcome.rejected]);
  });

  test('each file, and the whole: pause, resume, skip, cancel', () async {
    final (core, t, _) = _model();
    for (final a in ['pause', 'resume', 'cancel', 'skip_file', 'pause_file', 'resume_file']) {
      core.on(a, (_) => const FakeAnswer());
    }
    await t.pause();
    await t.resume();
    await t.skip(2);
    await t.pauseFile(1);
    await t.resumeFile(1);
    await t.cancel();
    expect(core.calls.map((c) => '${c.action} ${c.payload}'), [
      'pause {}',
      'resume {}',
      'skip_file {item_index: 2}',
      'pause_file {item_index: 1}',
      'resume_file {item_index: 1}',
      'cancel {}',
    ]);
    t.take(const CoreEvent('file_offered', data: {'file_name': 'a', 'file_size': 1}));
    t.take(const CoreEvent('transfer_paused', data: {'message': 'Transfer paused by peer'}));
    expect(t.active!.paused, isTrue);
    t.take(const CoreEvent('transfer_resumed', data: {'message': 'Transfer resumed by peer'}));
    expect(t.active!.paused, isFalse);
  });

  test('WP-B.8: the transfer failed here or there, the session alive: it ends, and the user is told', () async {
    final (_, t, store) = _model();
    final notices = <TransferNotice>[];
    t.notices.listen(notices.add);
    t.take(const CoreEvent('file_offered', data: {'file_name': 'big.bin', 'file_size': 1 << 30}));
    t.take(const CoreEvent('transfer_accepted', data: {}));
    t.take(const CoreEvent('transfer_failed', data: {'transfer_id': 'x', 'error': 'no data connection', 'by': 'peer'}));
    expect(t.active!.stage, Stage.done);
    expect(t.active!.outcome, Outcome.failed);
    // A late one changes nothing.
    t.take(const CoreEvent('transfer_failed', data: {'error': 'again'}));
    await _settle();
    expect(notices.whereType<TransferStopped>().map((n) => n.error), ['no data connection']);
    expect(store.entries.single.outcome, Outcome.failed);
  });

  test('the disk full and a file that failed are said', () async {
    final (_, t, _) = _model();
    final notices = <TransferNotice>[];
    t.notices.listen(notices.add);
    t.take(CoreEvent('batch_offered', data: {
      'summary': 'x',
      'total_files': 2,
      'total_bytes': 2,
      'items': [_q(0, 'a', 1, 'pending'), _q(1, 'b', 1, 'pending')],
    }));
    t.take(CoreEvent('item_failed', data: {
      'item_index': 1,
      'error': 'permission denied',
      'items': [_q(0, 'a', 1, 'pending'), _q(1, 'b', 1, 'failed')],
    }));
    t.take(const CoreEvent('disk_full', data: {'save_dir': '/x'}));
    await _settle();
    expect(notices.first, isA<FileError>());
    expect((notices.first as FileError).file, 'b');
    expect((notices.last as DiskFull).folder, '/x');
  });

  test('the history keeps the last 100, newest first', () async {
    final (core, t, store) = _model();
    core.on('respond_offer', (_) => const FakeAnswer());
    store.entries = [
      for (var i = 0; i < 100; i++)
        HistoryEntry(at: DateTime(2026, 1, 1).add(Duration(minutes: i)), direction: Direction.sent, device: 'd$i', files: 1, bytes: 1, outcome: Outcome.completed),
    ];
    await t.loadHistory();
    t.take(const CoreEvent('incoming_offer', data: {'device_name': 'new', 'file_name': 'f', 'file_size': 1, 'total_files': 1}));
    await t.reject();
    expect(t.entries.length, 100);
    expect(t.entries.first.device, 'new');
    expect(t.entries.last.device, 'd98');
  });

  test('the history file: written whole, read back; a damaged one starts afresh', () async {
    final dir = await Directory.systemTemp.createTemp('medxfer-history');
    addTearDown(() => dir.delete(recursive: true));
    final store = FileHistoryStore(() async => dir.path);
    expect(await store.load(), isEmpty);
    final e = HistoryEntry(
      at: DateTime.utc(2026, 10, 20, 9),
      direction: Direction.received,
      device: 'Galaxy',
      files: 3,
      bytes: 3000,
      outcome: Outcome.partial,
      folder: r'C:\Users\a\medXfer',
      name: 'Photos',
    );
    await store.save([e]);
    final back = (await store.load()).single;
    expect((back.at, back.direction, back.device, back.files, back.bytes, back.outcome, back.folder, back.name),
        (e.at, e.direction, e.device, e.files, e.bytes, e.outcome, e.folder, e.name));
    await File('${dir.path}${Platform.pathSeparator}history.json').writeAsString('{not json');
    expect(await store.load(), isEmpty);
  });

  test('Android: the offer goes where the platform says, and the history keeps it', () async {
    final core = FakeCore()..on('respond_offer', (_) => const FakeAnswer());
    final store = MemoryHistoryStore();
    final t = Transfers(
      core,
      history: store,
      peerName: () => 'PC',
      downloadDir: () async => '/storage/emulated/0/medXfer',
      placeFor: (o) async => const ReceivePlace('/storage/emulated/0/medXfer/Photos'),
    );
    t.take(CoreEvent('incoming_offer', data: {
      'device_name': 'PC',
      'file_name': 'Vacances',
      'file_size': 2,
      'total_files': 2,
      'items': [_q(0, 'Vacances/a.jpg', 1, 'pending'), _q(1, 'Vacances/b.jpg', 1, 'pending')],
    }));
    await t.accept();
    expect(core.calls.last.payload, {'accept': true, 'save_dir': '/storage/emulated/0/medXfer/Photos'});
    t.take(CoreEvent('transfer_complete', data: {
      'items': [_q(0, 'Vacances/a.jpg', 1, 'completed'), _q(1, 'Vacances/b.jpg', 1, 'completed')],
    }));
    await _settle();
    await _settle();
    expect(store.entries.single.folder, '/storage/emulated/0/medXfer/Photos');
  });

  test('Android sends by descriptor: the open files go to the core as items', () async {
    final (core, t, _) = _model();
    core.on('send', (_) => const FakeAnswer());
    await t.sendPicked(const Picked(files: [OpenFile(fd: 87, name: 'IMG_0001.jpg'), OpenFile(fd: 88, name: 'note.pdf')]));
    expect(core.calls.single.payload, {
      'items': [
        {'fd': 87, 'name': 'IMG_0001.jpg'},
        {'fd': 88, 'name': 'note.pdf'},
      ],
    });
    core.calls.clear();
    await t.sendPicked(const Picked());
    expect(core.calls, isEmpty);
  });

  test("files without a common folder: the core's name, without its English count", () {
    final (_, t, _) = _model();
    t.take(CoreEvent('batch_offered', data: {
      'summary': 'medXfer 2026-10-20 10h56 (2 files)',
      'total_files': 2,
      'total_bytes': 2,
      'items': [_q(0, 'essai1.bin', 1, 'pending'), _q(1, 'essai2.bin', 1, 'pending')],
    }));
    expect(t.active!.name, 'medXfer 2026-10-20 10h56');
  });
}
