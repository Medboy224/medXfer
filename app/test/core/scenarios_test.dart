// DEV-17: the golden scenarios that `xfer ctl` replays against the real core drive the fake
// core too. Each request of a scenario, sent to FakeCore.fromScenario, gives back the events
// the scenario expects: the fake speaks the same dialogue as the core.
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';

/// Whether got holds every field of want; "*" matches any value.
bool matches(Object? want, Object? got) {
  if (want == '*') return got != null;
  if (want is Map) {
    return got is Map && want.entries.every((e) => matches(e.value, got[e.key]));
  }
  if (want is List) {
    if (got is! List || want.length != got.length) return false;
    for (var i = 0; i < want.length; i++) {
      if (!matches(want[i], got[i])) return false;
    }
    return true;
  }
  return want == got;
}

void main() {
  final dir = Directory('../cmd/xfer/testdata/scenarios');
  final scenarios = dir.existsSync()
      ? (dir.listSync().whereType<File>().where((f) => f.path.endsWith('.jsonl')).toList()
        ..sort((a, b) => a.path.compareTo(b.path)))
      : <File>[];

  test('the scenarios are there', () {
    expect(scenarios, isNotEmpty, reason: 'cmd/xfer/testdata/scenarios, next to app/');
  });

  for (final file in scenarios) {
    test('replays ${file.uri.pathSegments.last}', () async {
      final text = file.readAsStringSync();
      final core = FakeCore.fromScenario(text, vars: const {'outdir': '/tmp/out', 'configdir': '/tmp/cfg'});
      final seen = <CoreEvent>[];
      final sub = core.events.listen(seen.add);
      await core.start(const CoreConfig(dataDir: '/tmp/cfg', downloadDir: '/tmp/out'));
      await pumpEventQueue();

      // Each request in order; each expectation met by a reply to it or by an event.
      String? requestId;
      String? replyId;
      for (final line in const LineSplitter().convert(text).map((l) => l.trim()).where((l) => l.isNotEmpty)) {
        final m = jsonDecode(line) as Map<String, Object?>;
        if (m.containsKey('action')) {
          final payload = (m['payload'] as Map?)?.cast<String, Object?>();
          final reply = await core.call(m['action'] as String, payload);
          expect(reply.ok, isTrue, reason: 'request refused: $line');
          seen.addAll(reply.events);
          requestId = m['id'] as String?;
          replyId = reply.id;
          await pumpEventQueue();
        } else if (m['expect'] is Map) {
          final want = Map<String, Object?>.from(m['expect'] as Map);
          final wantId = want['id'];
          final hit = seen.any((e) =>
              (want['event'] == null || e.name == want['event']) && // no name: any event
              (wantId == null || (wantId == requestId && e.id == replyId)) &&
              matches(want['data'] ?? const {}, e.data ?? const {}));
          expect(hit, isTrue, reason: 'expectation not met: $line');
        }
      }
      await sub.cancel();
    });
  }
}
