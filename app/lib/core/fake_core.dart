/// A core without the library (DEV-17): it replays a recorded scenario, the same files that
/// `xfer ctl` replays against the real core (cmd/xfer/testdata/scenarios), or answers what a
/// test scripts with [FakeCore.on]. The interface's tests run on it, without Go or a network.
library;

import 'dart:async';
import 'dart:convert';

import 'core.dart';

/// What a scripted action answers: its replies, and the events everyone gets.
class FakeAnswer {
  const FakeAnswer({this.replies = const [], this.broadcast = const [], this.ok = true, this.error});

  final List<CoreEvent> replies;
  final List<CoreEvent> broadcast;
  final bool ok;
  final String? error;
}

/// One recorded request and what followed it.
class _Step {
  _Step(this.action, this.payload);

  final String action;
  final Object? payload;
  final replies = <CoreEvent>[];
  final broadcast = <CoreEvent>[];
}

class FakeCore implements Core {
  FakeCore();

  /// A core that replays scenario, a JSON-lines file of `xfer ctl`: the expectations before
  /// the first request are the events of the start; each request answers, in order, with the
  /// expectations that follow it (those carrying its id as replies, the others broadcast).
  /// `${outdir}` and `${configdir}` become [vars].
  factory FakeCore.fromScenario(String scenario, {Map<String, String> vars = const {}}) {
    final core = FakeCore();
    _Step? step;
    String? stepId;
    for (var line in const LineSplitter().convert(scenario)) {
      line = line.trim();
      if (line.isEmpty) continue;
      for (final v in vars.entries) {
        line = line.replaceAll('\${${v.key}}', v.value);
      }
      final m = jsonDecode(line) as Map<String, Object?>;
      if (m.containsKey('action')) {
        step = _Step(m['action'] as String, m['payload']);
        stepId = m['id'] as String?;
        core._steps.add(step);
      } else if (m['expect'] is Map<String, Object?>) {
        final x = m['expect'] as Map<String, Object?>;
        final e = CoreEvent(x['event'] as String? ?? '', id: x['id'] as String?, data: x['data']);
        if (step == null) {
          core._onStart.add(e);
        } else if (e.id != null && e.id == stepId) {
          step.replies.add(e);
        } else {
          step.broadcast.add(e);
        }
      }
    }
    return core;
  }

  final _events = StreamController<CoreEvent>.broadcast();
  final _onStart = <CoreEvent>[];
  final _steps = <_Step>[];
  final _scripted = <String, FakeAnswer Function(Map<String, Object?> payload)>{};
  int _played = 0;
  int _nextId = 0;
  bool _started = false;

  /// The actions called, in order: what a test checks the interface asked for.
  final calls = <({String action, Map<String, Object?> payload})>[];

  /// Answers action with answer, whatever the scenario says.
  void on(String action, FakeAnswer Function(Map<String, Object?> payload) answer) => _scripted[action] = answer;

  /// Sends e to every listener, as the core does when something happens.
  void emit(CoreEvent e) => _events.add(e);

  @override
  Stream<CoreEvent> get events => _events.stream;

  @override
  Future<CoreStart> start(CoreConfig config) async {
    if (_started) return const CoreStart(ok: true, alreadyStarted: true, schema: coreSchema);
    _started = true;
    scheduleMicrotask(() => _onStart.forEach(_events.add));
    return const CoreStart(ok: true, schema: coreSchema);
  }

  @override
  Future<CoreReply> call(String action, [Map<String, Object?>? payload]) async {
    final p = payload ?? const <String, Object?>{};
    calls.add((action: action, payload: p));
    final id = 'ui-${++_nextId}';
    final scripted = _scripted[action];
    if (scripted != null) {
      final a = scripted(p);
      scheduleMicrotask(() => a.broadcast.forEach(_events.add));
      return CoreReply(ok: a.ok, id: id, error: a.error, events: [
        for (final e in a.replies) CoreEvent(e.name, id: id, data: e.data),
      ]);
    }
    if (_played < _steps.length && _steps[_played].action == action) {
      final step = _steps[_played++];
      scheduleMicrotask(() => step.broadcast.forEach(_events.add));
      return CoreReply(ok: true, id: id, events: [
        for (final e in step.replies) CoreEvent(e.name, id: id, data: e.data),
      ]);
    }
    return CoreReply(ok: false, id: id, error: 'fake core: unexpected action $action');
  }

  @override
  Future<void> stop() async {
    _events.add(const CoreEvent(CoreEvent.stopped));
    _started = false;
  }
}
