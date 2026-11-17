/// The transfer under way, « Version 4 » (plan J8, WP-8.1): the speed in big figures, the bar of
/// the whole and one per file, pause and skip for each file and for everything, a cancel that
/// is asked first; at the end, what happened, and the received folder to open.
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../../app.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';
import '../../transfer/history.dart';
import '../../transfer/transfers.dart';
import '../../widgets/format.dart';
import '../../widgets/smooth_bar.dart';

class TransferScreen extends StatelessWidget {
  const TransferScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final t = state.transfers;
    final l = AppLocalizations.of(context);
    return ListenableBuilder(
      listenable: t,
      builder: (context, _) {
        final a = t.active;
        if (a == null) {
          // Dismissed, the route is leaving; gone otherwise (a send refused), it leaves too,
          // rather than a blank screen.
          WidgetsBinding.instance.addPostFrameCallback((_) {
            if (!context.mounted) return;
            if (ModalRoute.of(context)?.isCurrent ?? false) Navigator.of(context).pop();
          });
          return const SizedBox.shrink();
        }
        final title = a.direction == Direction.sent ? l.sendingTo(a.device) : l.receivingFrom(a.device);
        return PopScope(
          // Going back leaves the transfer running; the finished one is dismissed with it.
          onPopInvokedWithResult: (didPop, _) {
            if (didPop) t.dismiss();
          },
          child: Scaffold(
            appBar: AppBar(title: Text(title, overflow: TextOverflow.ellipsis)),
            body: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                _Header(transfer: a),
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
                  child: Text(l.filesDoneLine(a.items.length, a.count(ItemStatus.completed)), style: titleStyle(15)),
                ),
                Expanded(
                  child: ListView.builder(
                    key: const Key('transfer-items'),
                    padding: const EdgeInsets.fromLTRB(12, 0, 8, 8),
                    itemCount: a.items.length,
                    itemBuilder: (context, i) => _ItemRow(item: a.items[i], transfer: a),
                  ),
                ),
              ],
            ),
            // In the bottom bar, the notices float above the buttons, not over them.
            bottomNavigationBar: _Footer(transfer: a, canOpen: state.folderOpener.supported),
          ),
        );
      },
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({required this.transfer});

  final ActiveTransfer transfer;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final a = transfer;
    final String line;
    if (a.stage == Stage.offered) {
      line = l.waitingAnswer(a.device);
    } else if (a.stage == Stage.done) {
      line = switch (a.outcome) {
        Outcome.completed => l.outcomeCompleted,
        Outcome.partial => l.outcomePartial,
        Outcome.canceled => l.outcomeCanceled,
        Outcome.rejected => l.outcomeRejected,
        _ => l.outcomeFailed,
      };
    } else if (a.lost) {
      // The data may go on without the session (WP-B.8): it says so rather than "waiting".
      line = a.speedMBps > 0 ? l.connectionLostGoingOn(a.device) : l.connectionLostWaiting(a.device);
    } else if (a.paused) {
      line = l.pausedLabel;
    } else {
      line = a.speedMBps > 0 ? l.speedLine(formatBytes(context, a.speedMBps * 1024 * 1024), formatDuration(context, a.etaSeconds)) : '';
    }
    // The speed in big figures while data flows (V4); a dash otherwise.
    // Until the core's first speed (it needs a few samples), the one since the start: the
    // figure moves with the bar from the first bytes (real test of 17 November).
    final live = a.speedMBps > 0 ? a.speedMBps : (a.sinceStartMBps ?? 0);
    final flowing = a.stage == Stage.running && !a.paused && live > 0;
    final locale = Localizations.localeOf(context).toLanguageTag();
    String figure(double v) => NumberFormat(v >= 100 ? '0' : '0.0', locale).format(v);
    // Ended: the average of the whole transfer; waiting or paused: 0.
    final average = a.stage == Stage.done && a.outcome == Outcome.completed ? a.averageMBps : null;
    final speed = flowing ? figure(live) : (average != null ? figure(average) : '0');
    final received = a.direction == Direction.received;
    final stat = TextStyle(fontSize: 19, fontWeight: FontWeight.w800, fontFeatures: tabular, color: c.text);
    final label = TextStyle(fontSize: 13, color: c.muted);
    return Container(
      margin: const EdgeInsets.fromLTRB(12, 8, 12, 0),
      padding: const EdgeInsets.fromLTRB(18, 16, 18, 18),
      decoration: BoxDecoration(color: c.surface, borderRadius: BorderRadius.circular(24)),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Container(
                padding: const EdgeInsets.fromLTRB(6, 4, 10, 4),
                decoration: BoxDecoration(color: c.strong, borderRadius: BorderRadius.circular(9)),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(received ? Icons.south_west_rounded : Icons.north_east_rounded, size: 16, color: c.onStrong),
                    const SizedBox(width: 4),
                    Text(received ? l.directionReceive : l.directionSend, style: TextStyle(color: c.onStrong, fontSize: 13, fontWeight: FontWeight.w800)),
                  ],
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Text(
                  line,
                  key: const Key('transfer-line'),
                  textAlign: TextAlign.end,
                  maxLines: 2,
                  style: TextStyle(color: a.outcome == Outcome.failed ? c.danger : c.muted, fontSize: 13, fontWeight: FontWeight.w600),
                ),
              ),
            ],
          ),
          const SizedBox(height: 12),
          Semantics(
            label: flowing || average != null ? '$speed ${l.speedUnit}' : null,
            excludeSemantics: flowing || average != null,
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.baseline,
              textBaseline: TextBaseline.alphabetic,
              children: [
                Flexible(
                  child: FittedBox(
                    fit: BoxFit.scaleDown,
                    alignment: Alignment.centerLeft,
                    child: Text(
                      speed,
                      key: const Key('transfer-speed'),
                      style: titleStyle(72, weight: FontWeight.w900, color: flowing || average != null ? c.accent : c.muted)
                          .copyWith(fontStyle: FontStyle.italic, height: 1, fontFeatures: tabular),
                    ),
                  ),
                ),
                const SizedBox(width: 10),
                Flexible(child: Text(average != null ? l.speedAverage(l.speedUnit) : l.speedUnit, style: titleStyle(20, color: c.muted))),
              ],
            ),
          ),
          const SizedBox(height: 14),
          SmoothBar(key: const Key('transfer-bar'), value: a.stage == Stage.offered ? null : a.fraction, minHeight: 14, color: a.paused ? c.muted : c.accent),
          const SizedBox(height: 12),
          Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [Text(l.progressLabel, style: label), Text('${(a.fraction * 100).toStringAsFixed(0)} %', style: stat)],
                ),
              ),
              Expanded(
                flex: 2,
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(l.transferredLabel, style: label),
                    FittedBox(
                      fit: BoxFit.scaleDown,
                      alignment: Alignment.centerLeft,
                      child: Text(l.progressLine(formatBytes(context, a.bytes), formatBytes(context, a.totalBytes)), style: stat),
                    ),
                  ],
                ),
              ),
              Column(
                crossAxisAlignment: CrossAxisAlignment.end,
                children: [Text(l.remainingLabel, style: label), Text(flowing ? formatDuration(context, a.etaSeconds) : '—', style: stat)],
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _ItemRow extends StatelessWidget {
  const _ItemRow({required this.item, required this.transfer});

  final TransferItem item;
  final ActiveTransfer transfer;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final t = AppScope.of(context).state.transfers;
    final running = transfer.stage == Stage.running;
    final open = item.status == ItemStatus.pending || item.status == ItemStatus.transferring || item.status == ItemStatus.paused;
    final received = transfer.direction == Direction.received;
    final (label, color, icon) = switch (item.status) {
      ItemStatus.completed => (l.statusCompleted, c.accent, Icons.check_rounded),
      ItemStatus.skipped => (l.statusSkipped, c.muted, Icons.skip_next_rounded),
      ItemStatus.failed => (l.statusFailed, c.danger, Icons.error_outline_rounded),
      ItemStatus.paused => (l.pausedLabel, c.muted, Icons.pause_rounded),
      ItemStatus.pending => (l.statusPending, c.muted, Icons.schedule_rounded),
      ItemStatus.transferring => (
        '${(item.fraction * 100).toStringAsFixed(0)} %',
        c.accent,
        received ? Icons.south_west_rounded : Icons.north_east_rounded,
      ),
    };
    final skipped = item.status == ItemStatus.skipped;
    final bar = item.status == ItemStatus.transferring || item.status == ItemStatus.paused;
    final small = IconButton.styleFrom(backgroundColor: c.surface, foregroundColor: c.text);
    return Opacity(
      key: ValueKey('item-${item.index}'),
      opacity: skipped ? 0.6 : 1,
      child: ConstrainedBox(
        constraints: const BoxConstraints(minHeight: 56),
        child: Row(
          children: [
            SizedBox(width: 32, child: Icon(icon, size: 20, color: color)),
            const SizedBox(width: 6),
            Expanded(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          item.path,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(fontWeight: FontWeight.w600, fontSize: 14, decoration: skipped ? TextDecoration.lineThrough : null),
                        ),
                      ),
                      const SizedBox(width: 8),
                      Text(
                        item.status == ItemStatus.completed ? formatBytes(context, item.size) : label,
                        style: TextStyle(color: c.muted, fontSize: 13, fontWeight: FontWeight.w600, fontFeatures: tabular),
                      ),
                    ],
                  ),
                  if (bar) ...[
                    const SizedBox(height: 6),
                    SmoothBar(value: item.fraction, minHeight: 4, color: item.status == ItemStatus.paused ? c.muted : c.accent),
                  ],
                ],
              ),
            ),
            if (running && open && transfer.items.length > 1) ...[
              const SizedBox(width: 4),
              if (item.status == ItemStatus.paused)
                IconButton.filledTonal(
                  key: ValueKey('resume-${item.index}'),
                  tooltip: l.resumeFile,
                  style: small,
                  icon: const Icon(Icons.play_arrow_rounded),
                  onPressed: () => unawaited(t.resumeFile(item.index)),
                )
              else
                IconButton.filledTonal(
                  key: ValueKey('pause-${item.index}'),
                  tooltip: l.pauseFile,
                  style: small,
                  icon: const Icon(Icons.pause_rounded),
                  onPressed: () => unawaited(t.pauseFile(item.index)),
                ),
              // « Passer » has its own sign, never the cross of « Annuler » (V4).
              IconButton.filledTonal(
                key: ValueKey('skip-${item.index}'),
                tooltip: l.skipFile,
                style: small,
                icon: const Icon(Icons.skip_next_rounded),
                onPressed: () => unawaited(t.skip(item.index)),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class _Footer extends StatelessWidget {
  const _Footer({required this.transfer, required this.canOpen});

  final ActiveTransfer transfer;
  final bool canOpen;

  /// Asked first: a cancel by mistake loses what is not finished (V4).
  Future<void> _cancel(BuildContext context) async {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final t = AppScope.of(context, listen: false).state.transfers;
    final a = transfer;
    final received = a.direction == Direction.received;
    final left = a.items.where((i) => i.status == ItemStatus.pending || i.status == ItemStatus.transferring || i.status == ItemStatus.paused).length;
    final yes = await showModalBottomSheet<bool>(
      context: context,
      builder: (context) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 20, 16, 16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(received ? l.cancelReceiveTitle : l.cancelSendTitle, style: titleStyle(20, weight: FontWeight.w900)),
              const SizedBox(height: 8),
              Text(l.cancelUnfinished(left), style: TextStyle(color: c.muted, fontSize: 14, height: 1.4)),
              const SizedBox(height: 16),
              FilledButton(
                key: const Key('transfer-keep'),
                style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(56)),
                onPressed: () => Navigator.of(context).pop(false),
                child: Text(l.keepTransfer),
              ),
              const SizedBox(height: 10),
              OutlinedButton(
                key: const Key('transfer-cancel-yes'),
                style: OutlinedButton.styleFrom(
                  minimumSize: const Size.fromHeight(56),
                  foregroundColor: c.danger,
                  side: BorderSide(color: c.danger, width: 2),
                ),
                onPressed: () => Navigator.of(context).pop(true),
                child: Text(received ? l.cancelReceive : l.cancelSend),
              ),
            ],
          ),
        ),
      ),
    );
    if (yes ?? false) await t.cancel();
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final state = AppScope.of(context).state;
    final t = state.transfers;
    final a = transfer;
    final done = a.stage == Stage.done;
    const big = Size.fromHeight(56);
    return SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(12, 10, 12, 16),
        child: done
            ? Wrap(
                alignment: WrapAlignment.end,
                spacing: 8,
                runSpacing: 8,
                children: [
                  if (canOpen && a.direction == Direction.received && a.outcome != Outcome.rejected)
                    TextButton.icon(
                      key: const Key('open-folder'),
                      onPressed: () async => state.folderOpener.open(t.receiveDir ?? await state.folders.downloadDir()),
                      icon: const Icon(Icons.folder_open_rounded),
                      label: Text(l.openFolder),
                    ),
                  if (t.canResend)
                    OutlinedButton.icon(
                      key: const Key('transfer-resend'),
                      onPressed: () => unawaited(t.resend()),
                      icon: const Icon(Icons.replay_rounded),
                      label: Text(l.resendTransfer),
                    ),
                  FilledButton(key: const Key('transfer-close'), onPressed: () => Navigator.of(context).maybePop(), child: Text(l.close)),
                ],
              )
            : Row(
                children: [
                  if (a.stage == Stage.running) ...[
                    Expanded(
                      child: FilledButton.icon(
                        key: const Key('transfer-pause'),
                        style: FilledButton.styleFrom(minimumSize: big, backgroundColor: c.section, foregroundColor: c.text),
                        onPressed: () => unawaited(a.paused ? t.resume() : t.pause()),
                        icon: Icon(a.paused ? Icons.play_arrow_rounded : Icons.pause_rounded),
                        label: Text(a.paused ? l.resumeAll : l.pauseAll),
                      ),
                    ),
                    const SizedBox(width: 8),
                  ],
                  Expanded(
                    child: OutlinedButton.icon(
                      key: const Key('transfer-cancel'),
                      style: OutlinedButton.styleFrom(minimumSize: big, foregroundColor: c.danger, side: BorderSide(color: c.danger, width: 2)),
                      onPressed: () => unawaited(_cancel(context)),
                      icon: const Icon(Icons.close_rounded),
                      label: Text(l.cancel),
                    ),
                  ),
                ],
              ),
      ),
    );
  }
}
