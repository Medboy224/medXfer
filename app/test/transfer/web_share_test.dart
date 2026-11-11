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
  'items': [
    {'id': '0', 'name': 'f.pdf', 'size': 3, 'folder': false},
  ],
});

const _photo = Pick(path: '/storage/DCIM/p.jpg', name: 'p.jpg', size: 5, kind: PickKind.media);

Future<void> _settle() async {
  for (var i = 0; i < 5; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

void main() {
  test('J7: what is chosen is kept before the session, edited, then offered at the start', () async {
    final core = FakeCore()..on('web_share_start', (_) => const FakeAnswer(replies: [_started]));
    final closed = <int>[];
    final ws = WebShare(
      core,
      // Android: the home's picks opened by the app at the start only.
      openPicks: (picks) async => (paths: const <String>[], files: [for (final (i, p) in picks.indexed) OpenFile(fd: 70 + i, name: p.name)]),
      closeFiles: (files) async => closed.addAll([for (final f in files) f.fd]),
    );
    addTearDown(ws.dispose);
    ws
      ..addPaths(['/home/a/f.pdf', '/home/a/f.pdf']) // twice: once
      ..addFiles(const [OpenFile(fd: 7, name: 'v.mp4'), OpenFile(fd: 8, name: 'x.mp4')])
      ..addPicks(const [_photo, _photo])
      ..receive = true
      ..autoAccept = true;
    expect([for (final d in ws.draft) d.name], ['f.pdf', 'v.mp4', 'x.mp4', 'p.jpg']);
    await ws.removeDraft(2); // x.mp4: open already, closed
    expect(closed, [8]);
    ws.receive = false;
    expect(ws.autoAccept, isFalse, reason: 'nothing to accept without receiving');
    ws.receive = true;

    await ws.start(address: '192.168.1.20');
    expect(core.calls.single.payload, {
      'paths': ['/home/a/f.pdf'],
      'items': [
        {'fd': 7, 'name': 'v.mp4'},
        {'fd': 70, 'name': 'p.jpg'},
      ],
      'receive': true,
      'auto_accept': true,
      'addr': '192.168.1.20',
    });
    final s = ws.session!;
    expect((s.pinShown, s.address, s.receive, ws.active, ws.draft.isEmpty), ('054 461', 'https://192.168.1.20:19999', true, true, true));
    expect([for (final it in ws.items) '${it.id}:${it.name}'], ['0:f.pdf']);
  });

  test('J7: the session running, more offered and some withdrawn; the home\'s selection added to it', () async {
    final core = FakeCore()
      ..on('web_share_start', (_) => const FakeAnswer(replies: [_started]))
      ..on('web_share_add', (_) => const FakeAnswer(replies: [
            CoreEvent('web_share_added', data: {
              'count': 2,
              'items': [
                {'id': '1', 'name': 'g.pdf', 'size': 4},
              ],
            }),
          ]))
      ..on('web_share_remove', (p) => FakeAnswer(replies: [CoreEvent('web_share_removed', data: {'id': p['id']})]));
    final ws = WebShare(core);
    addTearDown(ws.dispose);
    expect(await ws.add(paths: ['/x']), isFalse, reason: 'no session yet');
    // Without a session, the home's selection waits on the screen, which is asked for.
    expect(await ws.offerSelection(const [_photo]), isTrue);
    expect(ws.wanted, isTrue);
    expect([for (final d in ws.draft) d.name], ['p.jpg']);
    ws.shown();
    expect(ws.wanted, isFalse);

    await ws.start();
    expect(await ws.offerSelection(const [_photo]), isTrue); // running: added (by path on a desktop)
    expect(core.calls.last.payload, {
      'paths': ['/storage/DCIM/p.jpg'],
    });
    expect([for (final it in ws.items) it.name], ['f.pdf', 'g.pdf']);
    await ws.remove('0');
    expect(core.calls.last.payload, {'id': '0'});
    expect([for (final it in ws.items) it.id], ['1']);

    core.on('web_share_add', (_) => const FakeAnswer(replies: [CoreEvent('action_error', data: {'error': 'webshare: x is not a regular file'})]));
    expect(await ws.add(paths: ['/dev/x']), isFalse);
    expect((ws.error, ws.items.length), ('webshare: x is not a regular file', 1));
  });

  test('J7: the guest\'s upload asked, accepted, followed, and the session\'s end said', () async {
    final core = FakeCore()
      ..on('web_share_start', (_) => const FakeAnswer(replies: [_started]))
      ..on('web_share_answer', (_) => const FakeAnswer());
    final ws = WebShare(core);
    addTearDown(ws.dispose);
    await ws.start();
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
    core
      ..emit(const CoreEvent('web_share_upload_file_done', data: {'ticket': 't1', 'name': 'b (1).jpg'}))
      ..emit(const CoreEvent('web_share_upload_done', data: {'ticket': 't1'}))
      ..emit(const CoreEvent('web_share_download_done', data: {'id': '0'}));
    await _settle();
    expect((ws.upload, ws.downloads), (null, 1));
    expect(ws.received, ['a.jpg', 'b (1).jpg']);

    core.emit(const CoreEvent('web_share_closed', data: {'reason': 'expired'}));
    await _settle();
    expect((ws.session, ws.ended, ws.active, ws.items.isEmpty), (null, WebShareEnd.expired, false, true));
  });

  test('J7: refused at once on screen; a start the core refuses is said, the open files not kept', () async {
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

    core
      ..on('web_share_start', (_) => const FakeAnswer(replies: [CoreEvent('action_error', data: {'error': 'no address'})]))
      ..emit(const CoreEvent('web_share_closed', data: {'reason': 'pin_attempts'}));
    await _settle();
    expect(ws.ended, WebShareEnd.pinAttempts);
    ws
      ..addPaths(['/home/a/f.pdf'])
      ..addFiles(const [OpenFile(fd: 7, name: 'v.mp4')]);
    await ws.start();
    expect((ws.session, ws.error), (null, 'no address'));
    // The core closed the file it was handed; the path stays, to try again.
    expect([for (final d in ws.draft) d.name], ['f.pdf']);
  });

  test('J7: the guest\'s network: listed Wi-Fi first, chosen, used at the start; gone, the first again (WP-W.6)', () async {
    final core = FakeCore()
      ..on('web_share_networks', (_) => const FakeAnswer(replies: [
            CoreEvent('web_share_networks', data: {
              'networks': [
                {'ip': '192.168.49.174', 'kind': 'wifi'},
                {'ip': '192.168.81.175', 'kind': 'ethernet'},
              ],
            }),
          ]))
      ..on('web_share_start', (_) => const FakeAnswer(replies: [_started]));
    final ws = WebShare(core);
    addTearDown(ws.dispose);
    expect(ws.network, isNull);
    await ws.loadNetworks();
    expect([for (final n in ws.networks) '${n.kind} ${n.ip}'], ['wifi 192.168.49.174', 'ethernet 192.168.81.175']);
    expect(ws.network, '192.168.49.174');
    ws.network = '10.31.5.248'; // not among them: the first
    expect(ws.network, '192.168.49.174');
    ws
      ..network = '192.168.81.175'
      ..addPaths(['/home/a/f.pdf']);
    await ws.start();
    expect(core.calls.last.payload['addr'], '192.168.81.175');
  });
}