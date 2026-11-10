import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';
import 'package:medxfer/library/selection.dart';
import 'package:medxfer/platform/file_picker.dart';
import 'package:medxfer/transfer/web_share.dart';

const _started = CoreEvent('web_share_started', data: {
  'url': 'https://192.168.1.20:19999/s#t=0123456789abcdef0123456789abcdef',
  'token': '0123456789abcdef0123456789abcdef',
  'pin': '054461',
  'fingerprint': '9E:81',
  'addr': '192.168.1.20',
  'port': 19999,
  'receive': true,
  'expires': '2026-11-10T12:00:00Z',
});

Future<void> _settle() async {
  for (var i = 0; i < 5; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

void main() {
  test('J7: the session started, the guest\'s upload asked, accepted, followed, and its end said', () async {
    final core = FakeCore()
      ..on('web_share_start', (_) => const FakeAnswer(replies: [_started]))
      ..on('web_share_answer', (_) => const FakeAnswer())
      ..on('web_share_stop', (_) => const FakeAnswer(broadcast: [CoreEvent('web_share_closed', data: {'reason': 'stopped'})]));
    final ws = WebShare(core);
    addTearDown(ws.dispose);
    await ws.start(paths: ['/home/a/f.pdf'], files: const [OpenFile(fd: 7, name: 'v.mp4')], autoAccept: false, address: '192.168.1.20');
    expect(core.calls.single.payload, {
      'paths': ['/home/a/f.pdf'],
      'items': [
        {'fd': 7, 'name': 'v.mp4'},
      ],
      'receive': true,
      'auto_accept': false,
      'addr': '192.168.1.20',
    });
    final s = ws.session!;
    expect((s.pinShown, s.address, s.receive, ws.active), ('054 461', 'https://192.168.1.20:19999', true, true));

    core.emit(const CoreEvent('web_share_upload_request', data: {
      'ticket': 't1',
      'total': 30,
      'auto': false,
      'files': [
        {'name': 'a.jpg', 'size': 10},
        {'name': 'b.jpg', 'size': 20},
      ],
    }));
    await _settle();
    expect((ws.upload!.files.length, ws.upload!.accepted), (2, false));
    await ws.answer(true);
    expect(core.calls.last.payload, {'ticket': 't1', 'accept': true});
    core
      ..emit(const CoreEvent('web_share_upload_accepted', data: {'ticket': 't1'}))
      ..emit(const CoreEvent('web_share_upload_progress', data: {'ticket': 't1', 'received': 12, 'total': 30}))
      ..emit(const CoreEvent('web_share_upload_file_done', data: {'ticket': 't1', 'name': 'a.jpg'}));
    await _settle();
    expect((ws.upload!.accepted, ws.upload!.received), (true, 12));
    expect(ws.received, ['a.jpg']);
    core
      ..emit(const CoreEvent('web_share_upload_file_done', data: {'ticket': 't1', 'name': 'b (1).jpg'}))
      ..emit(const CoreEvent('web_share_upload_done', data: {'ticket': 't1'}))
      ..emit(const CoreEvent('web_share_download_done', data: {'id': '0'}));
    await _settle();
    expect((ws.upload, ws.downloads), (null, 1));
    expect(ws.received, ['a.jpg', 'b (1).jpg']);

    core.emit(const CoreEvent('web_share_closed', data: {'reason': 'expired'}));
    await _settle();
    expect((ws.session, ws.ended, ws.active), (null, WebShareEnd.expired, false));
  });

  test('J7: more offered during the session; the home\'s selection waits for the screen', () async {
    final core = FakeCore()
      ..on('web_share_start', (_) => const FakeAnswer(replies: [_started]))
      ..on('web_share_add', (_) => const FakeAnswer(replies: [CoreEvent('web_share_added', data: {'count': 3})]));
    final ws = WebShare(core);
    addTearDown(ws.dispose);
    expect(await ws.add(paths: ['/x']), isFalse, reason: 'no session yet');
    ws.offer(const [Pick(path: '/home/a/b.pdf', name: 'b.pdf', size: 3, kind: PickKind.file)]);
    expect(ws.wanted, isTrue);
    expect([for (final p in ws.takePending()) p.name], ['b.pdf']);
    expect(ws.wanted, isFalse);
    expect(ws.takePending(), isEmpty);
    await ws.start(paths: ['/home/a/f.pdf'], names: ['f.pdf']);
    expect(await ws.add(paths: ['/home/a/g.pdf'], files: const [OpenFile(fd: 9, name: 'h.mp4')], names: ['g.pdf', 'h.mp4']), isTrue);
    expect(core.calls.last.payload, {
      'paths': ['/home/a/g.pdf'],
      'items': [
        {'fd': 9, 'name': 'h.mp4'},
      ],
    });
    expect(ws.offered, ['f.pdf', 'g.pdf', 'h.mp4']);
    core.on('web_share_add', (_) => const FakeAnswer(replies: [CoreEvent('action_error', data: {'error': 'webshare: x is not a regular file'})]));
    expect(await ws.add(paths: ['/dev/x'], names: ['x']), isFalse);
    expect((ws.error, ws.offered.length), ('webshare: x is not a regular file', 3));
  });

  test('J7: refused at once on screen; a start the core refuses is said', () async {
    final core = FakeCore()
      ..on('web_share_start', (_) => const FakeAnswer(replies: [_started]))
      ..on('web_share_answer', (_) => const FakeAnswer());
    final ws = WebShare(core);
    addTearDown(ws.dispose);
    await ws.start();
    core.emit(const CoreEvent('web_share_upload_request', data: {'ticket': 't2', 'total': 1, 'files': []}));
    await _settle();
    await ws.answer(false);
    expect(ws.upload, isNull);
    expect(core.calls.last.payload, {'ticket': 't2', 'accept': false});

    core.on('web_share_start', (_) => const FakeAnswer(replies: [CoreEvent('action_error', data: {'error': 'no address'})]));
    core.emit(const CoreEvent('web_share_closed', data: {'reason': 'pin_attempts'}));
    await _settle();
    expect(ws.ended, WebShareEnd.pinAttempts);
    await ws.start();
    expect((ws.session, ws.error), (null, 'no address'));
  });
}
