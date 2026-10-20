/// The developer panel (DEV-13, DEV-19-R): the live transfer as the core measures it, the disk
/// and network benchmarks, the last transfer's report, and the diagnostic bundle. Pairing,
/// sending and receiving are the app's own screens.
library;

import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app.dart';
import '../../dev/dev_tools.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';
import '../../widgets/format.dart';

/// The stages in the order of a chunk's way: the receiver's, then the sender's.
const _stageOrder = ['net_wait', 'hash', 'disk_write', 'fsync', 'disk_read', 'net_write'];

String _stageName(AppLocalizations l, String stage) => switch (stage) {
  'net_wait' => l.stageNetWait,
  'hash' => l.stageHash,
  'disk_write' => l.stageDiskWrite,
  'fsync' => l.stageFsync,
  'disk_read' => l.stageDiskRead,
  'net_write' => l.stageNetWrite,
  _ => stage,
};

String _confidence(AppLocalizations l, String c) => switch (c) {
  'high' => l.confidenceHigh,
  'medium' => l.confidenceMedium,
  _ => l.confidenceLow,
};

/// A speed in MiB/s as the user reads it, without the "/s".
String _mbps(BuildContext context, double mbps) => formatBytes(context, mbps * 1024 * 1024);

class DevPanel extends StatelessWidget {
  const DevPanel({super.key});

  static const routeName = 'dev-panel';

  /// Opens the panel, unless it is already the screen shown (the banner stays under it).
  static void open(NavigatorState navigator) {
    var shown = false;
    navigator.popUntil((route) {
      shown = route.settings.name == routeName;
      return true; // reads the top route, pops nothing
    });
    if (shown) return;
    unawaited(navigator.push(MaterialPageRoute<void>(settings: const RouteSettings(name: routeName), builder: (_) => const DevPanel())));
  }

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    return Scaffold(
      appBar: AppBar(title: Text(l.devPanel)),
      body: ListenableBuilder(
        listenable: Listenable.merge([state.dev, state.connection]),
        builder: (context, _) {
          final dev = state.dev;
          return ListView(
            padding: const EdgeInsets.only(bottom: 24),
            children: [
              _Section(title: l.devLive, child: _Live(dev: dev)),
              _Section(
                title: l.devBench,
                child: Column(
                  children: [
                    _Job(
                      key: const Key('bench-disk'),
                      title: l.devDisk,
                      running: dev.running(DevJob.disk),
                      error: dev.error(DevJob.disk),
                      result: dev.disk == null
                          ? null
                          : [
                              l.devDiskResult(_mbps(context, dev.disk!.writeMBps), _mbps(context, dev.disk!.readMBps)),
                              if (dev.disk!.warning.isNotEmpty) dev.disk!.warning,
                            ].join('\n'),
                      onRun: dev.runDisk,
                    ),
                    _Job(
                      key: const Key('bench-network'),
                      title: l.devNetwork,
                      running: dev.running(DevJob.network),
                      error: dev.error(DevJob.network),
                      result: dev.net == null
                          ? (state.connection.session == null ? l.devNeedsDevice : null)
                          : l.devNetResult(
                              _mbps(context, dev.net!.throughputMBps),
                              dev.net!.avgRttMs.toStringAsFixed(1),
                              dev.net!.minRttMs.toStringAsFixed(1),
                              dev.net!.maxRttMs.toStringAsFixed(1),
                            ),
                      onRun: state.connection.session == null ? null : dev.runNetwork,
                    ),
                  ],
                ),
              ),
              _Section(title: l.devReport, child: _Report(dev: dev)),
              _Section(
                title: l.devBundle,
                child: _Job(
                  key: const Key('bundle'),
                  title: l.devBundleHint,
                  action: l.devCreate,
                  running: dev.running(DevJob.bundle),
                  error: dev.error(DevJob.bundle),
                  result: dev.bundle == null ? null : l.devSaved(dev.bundle!),
                  onRun: dev.exportBundle,
                  extra: dev.bundle == null
                      ? null
                      : TextButton(
                          onPressed: () => unawaited(state.folderOpener.open(File(dev.bundle!).parent.path)),
                          child: Text(l.openFolder),
                        ),
                ),
              ),
            ],
          );
        },
      ),
    );
  }
}

class _Section extends StatelessWidget {
  const _Section({required this.title, required this.child});

  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(title, style: TextStyle(color: c.muted, fontWeight: FontWeight.w700)),
          const SizedBox(height: 8),
          DecoratedBox(
            decoration: BoxDecoration(color: c.section, borderRadius: BorderRadius.circular(12)),
            child: Padding(padding: const EdgeInsets.all(12), child: child),
          ),
        ],
      ),
    );
  }
}

/// The transfer's speed, its curve, and its stages with the limiting one.
class _Live extends StatelessWidget {
  const _Live({required this.dev});

  final DevTools dev;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final t = dev.live;
    if (t == null) return Text(l.devNoTransfer, style: TextStyle(color: c.muted));
    final accent = Theme.of(context).colorScheme.primary;
    final stages = [for (final s in _stageOrder) if (t.stages.containsKey(s)) s];
    final top = stages.isEmpty ? 1.0 : stages.map((s) => t.stages[s]!).reduce((a, b) => a > b ? a : b);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('${_mbps(context, t.speedMBps)}/s', key: const Key('live-speed'), style: const TextStyle(fontSize: 28, fontWeight: FontWeight.w800)),
        Text(l.devBytes(formatBytes(context, t.bytes), formatBytes(context, t.totalBytes)), style: TextStyle(color: c.muted)),
        const SizedBox(height: 8),
        SizedBox(height: 64, width: double.infinity, child: CustomPaint(painter: _Curve(dev.speeds, accent, c.border))),
        if (stages.isNotEmpty) ...[
          const SizedBox(height: 12),
          Text(l.devStages, style: TextStyle(color: c.muted, fontSize: 12)),
          const SizedBox(height: 4),
          for (final s in stages)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 3),
              child: Row(
                children: [
                  SizedBox(
                    width: 150,
                    child: Text(
                      _stageName(l, s),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(fontWeight: s == t.bottleneck ? FontWeight.w800 : FontWeight.w400),
                    ),
                  ),
                  Expanded(
                    child: ClipRRect(
                      borderRadius: BorderRadius.circular(3),
                      child: LinearProgressIndicator(
                        value: top <= 0 ? 0 : t.stages[s]! / top,
                        minHeight: 6,
                        color: s == t.bottleneck ? c.danger : accent,
                        backgroundColor: c.chip,
                      ),
                    ),
                  ),
                  SizedBox(
                    width: 72,
                    child: Text('${t.stages[s]!.toStringAsFixed(0)} ms', textAlign: TextAlign.end, style: TextStyle(color: c.muted, fontSize: 12)),
                  ),
                ],
              ),
            ),
        ],
        if (t.bottleneck.isNotEmpty) ...[
          const SizedBox(height: 8),
          Text(
            l.devBottleneck(_stageName(l, t.bottleneck), _confidence(l, t.confidence)),
            key: const Key('live-bottleneck'),
            style: const TextStyle(fontWeight: FontWeight.w700),
          ),
        ],
      ],
    );
  }
}

/// The speeds of the transfer, oldest on the left, scaled to the fastest.
class _Curve extends CustomPainter {
  _Curve(this.speeds, this.color, this.grid);

  final List<double> speeds;
  final Color color;
  final Color grid;

  @override
  void paint(Canvas canvas, Size size) {
    canvas.drawLine(Offset(0, size.height), Offset(size.width, size.height), Paint()..color = grid);
    if (speeds.length < 2) return;
    final top = speeds.reduce((a, b) => a > b ? a : b);
    if (top <= 0) return;
    final step = size.width / (DevTools.samples - 1);
    final x0 = size.width - step * (speeds.length - 1); // the newest on the right edge
    final path = Path();
    for (final (i, v) in speeds.indexed) {
      final p = Offset(x0 + step * i, size.height - v / top * (size.height - 2));
      i == 0 ? path.moveTo(p.dx, p.dy) : path.lineTo(p.dx, p.dy);
    }
    canvas.drawPath(
      path,
      Paint()
        ..color = color
        ..strokeWidth = 2
        ..style = PaintingStyle.stroke,
    );
  }

  @override
  bool shouldRepaint(_Curve old) => true; // a new list on each event
}

/// One thing to run, with its result or its error.
class _Job extends StatelessWidget {
  const _Job({super.key, required this.title, required this.running, required this.onRun, this.result, this.error, this.action, this.extra});

  final String title;
  final bool running;
  final Future<void> Function()? onRun;
  final String? result;
  final String? error;
  final String? action;
  final Widget? extra;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(child: Text(title, style: const TextStyle(fontWeight: FontWeight.w600))),
              const SizedBox(width: 8),
              running
                  ? const SizedBox.square(dimension: 24, child: CircularProgressIndicator(strokeWidth: 2.5))
                  : FilledButton.tonal(onPressed: onRun == null ? null : () => unawaited(onRun!()), child: Text(action ?? l.devRun)),
            ],
          ),
          if (error != null) Text(l.devFailed(error!), style: TextStyle(color: c.danger)),
          if (result != null) SelectableText(result!, style: TextStyle(color: c.muted)),
          ?extra,
        ],
      ),
    );
  }
}

/// The last transfer's report, as the core writes it, to read or to copy.
class _Report extends StatelessWidget {
  const _Report({required this.dev});

  final DevTools dev;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final report = dev.report;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            FilledButton.tonal(
              key: const Key('report-show'),
              onPressed: dev.running(DevJob.report) ? null : () => unawaited(dev.loadReport()),
              child: Text(l.devShow),
            ),
            const SizedBox(width: 8),
            if (report != null && report.isNotEmpty)
              TextButton(
                onPressed: () {
                  unawaited(Clipboard.setData(ClipboardData(text: report)));
                  ScaffoldMessenger.of(context)
                    ..clearSnackBars()
                    ..showSnackBar(SnackBar(content: Text(l.copied)));
                },
                child: Text(l.copy),
              ),
          ],
        ),
        if (dev.error(DevJob.report) != null) Text(l.devFailed(dev.error(DevJob.report)!), style: TextStyle(color: c.danger)),
        if (report != null) ...[
          const SizedBox(height: 8),
          report.isEmpty
              ? Text(l.devNoReport, style: TextStyle(color: c.muted))
              : SingleChildScrollView(
                  scrollDirection: Axis.horizontal,
                  child: SelectableText(report, key: const Key('report-text'), style: const TextStyle(fontFamily: 'monospace', fontSize: 11)),
                ),
        ],
      ],
    );
  }
}
