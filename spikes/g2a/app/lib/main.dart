// G2a prototype app: one test screen with a log, no design (plan §2).
//
// Automatic mode, for unattended measurements:
//   desktop: MEDXFER_G2A_AUTORUN=quick|full  (results: MEDXFER_G2A_RESULT or <temp>/medxfer_g2a_results.json)
//   android: flutter build apk --dart-define=AUTORUN=quick|full  (results in logcat and the app's temp dir)
// The app runs the suite, writes the JSON results, prints "G2A_RESULT <json>" and exits
// with status 0 when every criterion passed.
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';

import 'core.dart';
import 'tests.dart';
import 'transfers.dart';

const _autorunDefine = String.fromEnvironment('AUTORUN');
const _sizeDefine = String.fromEnvironment('SIZE_MB', defaultValue: '1024');
const _peerDefine = String.fromEnvironment('PEER');
const _trialsDefine = String.fromEnvironment('TRIALS', defaultValue: '3');

/// Desktop: environment variable `MEDXFER_G2A_<name>`; Android: --dart-define value.
String _setting(String name, String define) => Platform.environment['MEDXFER_G2A_$name'] ?? define;

void main() {
  final sinceMain = Stopwatch()..start();
  WidgetsFlutterBinding.ensureInitialized();
  final core = Core.open();
  core.start({'data_dir': Directory.systemTemp.path, 'download_dir': Directory.systemTemp.path});
  final firstPing = core.call('ping');
  final coldStart = sinceMain.elapsed;
  final autorun = Platform.environment['MEDXFER_G2A_AUTORUN'] ?? _autorunDefine;
  runApp(G2aApp(core: core, coldStart: coldStart, firstPingOk: firstPing['ok'] == true, autorun: autorun));
}

class G2aApp extends StatelessWidget {
  const G2aApp({super.key, required this.core, required this.coldStart, required this.firstPingOk, required this.autorun});
  final Core core;
  final Duration coldStart;
  final bool firstPingOk;
  final String autorun;

  @override
  Widget build(BuildContext context) => MaterialApp(
        title: 'medXfer G2a',
        home: TestScreen(core: core, coldStart: coldStart, firstPingOk: firstPingOk, autorun: autorun),
      );
}

class TestScreen extends StatefulWidget {
  const TestScreen({super.key, required this.core, required this.coldStart, required this.firstPingOk, required this.autorun});
  final Core core;
  final Duration coldStart;
  final bool firstPingOk;
  final String autorun;

  @override
  State<TestScreen> createState() => _TestScreenState();
}

class _TestScreenState extends State<TestScreen> {
  final _lines = <String>[];
  bool _running = false;

  void _log(String s) {
    // ignore: avoid_print
    print('G2A $s');
    // MEDXFER_G2A_QUIET=1: log without redrawing the screen (G2A-06 investigation).
    if (Platform.environment['MEDXFER_G2A_QUIET'] == '1') return;
    if (mounted) setState(() => _lines.add(s));
  }

  @override
  void initState() {
    super.initState();
    _log('core loaded (${libraryName()}), first ping ${widget.firstPingOk ? 'ok' : 'FAILED'} '
        'after ${widget.coldStart.inMicroseconds / 1000} ms since main()');
    if (widget.autorun == 'quick' || widget.autorun == 'full') {
      WidgetsBinding.instance.addPostFrameCallback((_) => _run(full: widget.autorun == 'full', exitAfter: true));
    } else if (widget.autorun == 'loopback' || widget.autorun == 'g2a07') {
      // MEDXFER_G2A_DELAY_MS: start after the app's own start-up work (G2A-06 investigation).
      final delay = Duration(milliseconds: int.parse(Platform.environment['MEDXFER_G2A_DELAY_MS'] ?? '0'));
      WidgetsBinding.instance.addPostFrameCallback(
          (_) => Future<void>.delayed(delay, () => _runTransfers(widget.autorun, exitAfter: true)));
    }
  }

  Future<void> _run({required bool full, bool exitAfter = false}) async {
    setState(() => _running = true);
    final results = await Suite(widget.core, _log, full: full, only: Platform.environment['MEDXFER_G2A_ONLY'])
        .runAll(widget.coldStart);
    _finish(results, exitAfter);
  }

  // S4: loopback (G2A-06) or app <-> CLI between devices (G2A-07, needs PEER = host:port of `xfer send`).
  Future<void> _runTransfers(String mode, {bool exitAfter = false}) async {
    setState(() => _running = true);
    final sizeMB = int.parse(_setting('SIZE_MB', _sizeDefine));
    final t = Transfers(widget.core, _log);
    await t.open();
    Map<String, dynamic> r;
    try {
      r = mode == 'loopback'
          ? await t.loopback(
              sizeMB: sizeMB,
              trials: int.parse(_setting('TRIALS', _trialsDefine)),
              existing: Platform.environment['MEDXFER_G2A_SRC'])
          : await t.betweenDevices(sizeMB: sizeMB, peer: _setting('PEER', _peerDefine));
    } catch (e, st) {
      r = {'pass': false, 'exception': '$e', 'stack': '$st'};
    }
    await t.close();
    _log('$mode: ${r['pass'] == true ? 'PASS' : 'FAIL'} $r');
    _finish({'platform': Platform.operatingSystem, 'os_version': Platform.operatingSystemVersion, mode: r,
      'pass': r['pass'] == true}, exitAfter);
  }

  void _finish(Map<String, dynamic> results, bool exitAfter) {
    final json = jsonEncode(results);
    final path = Platform.environment['MEDXFER_G2A_RESULT'] ??
        '${Directory.systemTemp.path}${Platform.pathSeparator}medxfer_g2a_results.json';
    File(path).writeAsStringSync(const JsonEncoder.withIndent('  ').convert(results));
    if (Platform.isAndroid) {
      // logcat cuts lines near 1,000 characters: numbered chunks, joined by android-run.ps1.
      const size = 800;
      final n = (json.length + size - 1) ~/ size;
      for (var i = 0; i < n; i++) {
        // ignore: avoid_print
        print('G2A_RESULT_CHUNK ${i + 1}/$n ${json.substring(i * size, ((i + 1) * size).clamp(0, json.length))}');
      }
    } else {
      // ignore: avoid_print
      print('G2A_RESULT $json');
    }
    _log('results written to $path — overall ${results['pass'] == true ? 'PASS' : 'FAIL'}');
    if (mounted) setState(() => _running = false);
    if (exitAfter) exit(results['pass'] == true ? 0 : 1);
  }

  @override
  Widget build(BuildContext context) => Scaffold(
        appBar: AppBar(title: const Text('medXfer G2a prototype')),
        body: Column(children: [
          Padding(
            padding: const EdgeInsets.all(8),
            child: Wrap(spacing: 8, children: [
              ElevatedButton(onPressed: _running ? null : () => _run(full: false), child: const Text('Quick run')),
              ElevatedButton(onPressed: _running ? null : () => _run(full: true), child: const Text('Full run')),
              ElevatedButton(
                  onPressed: _running ? null : () => _runTransfers('loopback'), child: const Text('Loopback transfer')),
            ]),
          ),
          Expanded(
            child: ListView.builder(
              itemCount: _lines.length,
              itemBuilder: (_, i) => Text(_lines[i], style: const TextStyle(fontFamily: 'monospace', fontSize: 12)),
            ),
          ),
        ]),
      );
}
