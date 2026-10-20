/// The transfer under way (§1.2 of the J6b plan): the bar of the whole and one per file, pause
/// and skip for each file and for everything; at the end, what happened, and the received
/// folder to open.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';
import '../../transfer/history.dart';
import '../../transfer/transfers.dart';
import '../../widgets/format.dart';

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
        if (a == null) return const SizedBox.shrink(); // dismissed: the route is leaving
        final title = a.direction == Direction.sent ? l.sendingTo(a.device) : l.receivingFrom(a.device);
        return PopScope(
          // Going back leaves the transfer running; the finished one is dismissed with it.
          onPopInvokedWithResult: (didPop, _) {
            if (didPop) t.dismiss();
          },
          child: Scaffold(
            appBar: AppBar(title: Text(title, overflow: TextOverflow.ellipsis)),
            body: Column(
              children: [
                _Header(transfer: a),
                Expanded(
                  child: ListView.builder(
                    key: const Key('transfer-items'),
                    itemCount: a.items.length,
                    itemBuilder: (context, i) => _ItemRow(item: a.items[i], transfer: a),
                  ),
                ),
                _Footer(transfer: a, canOpen: state.folderOpener.supported),
              ],
            ),
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
    final reduce = MediaQuery.of(context).disableAnimations;
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
    } else if (a.paused) {
      line = l.pausedLabel;
    } else {
      line = a.speedMBps > 0 ? l.speedLine(formatBytes(context, a.speedMBps * 1024 * 1024), formatDuration(context, a.etaSeconds)) : '';
    }
    return Container(
      color: c.section,
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            a.name,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 16),
          ),
          const SizedBox(height: 8),
          TweenAnimationBuilder<double>(
            tween: Tween(end: a.stage == Stage.offered ? 0 : a.fraction),
            duration: reduce ? Duration.zero : const Duration(milliseconds: 250),
            builder: (context, v, _) => LinearProgressIndicator(
              key: const Key('transfer-bar'),
              value: a.stage == Stage.offered ? null : v,
              minHeight: 8,
              borderRadius: BorderRadius.circular(4),
            ),
          ),
          const SizedBox(height: 6),
          // Wrapped: on a narrow phone, the speed goes under the sizes.
          Wrap(
            alignment: WrapAlignment.spaceBetween,
            spacing: 12,
            children: [
              Text(
                '${l.filesCount(a.items.length)} · ${l.progressLine(formatBytes(context, a.bytes), formatBytes(context, a.totalBytes))}',
                style: TextStyle(color: c.muted, fontSize: 13),
              ),
              Text(
                line,
                key: const Key('transfer-line'),
                style: TextStyle(color: a.outcome == Outcome.failed ? c.danger : c.muted, fontSize: 13),
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
    final (label, color) = switch (item.status) {
      ItemStatus.completed => (l.statusCompleted, c.ok),
      ItemStatus.skipped => (l.statusSkipped, c.muted),
      ItemStatus.failed => (l.statusFailed, c.danger),
      ItemStatus.paused => (l.pausedLabel, c.muted),
      ItemStatus.pending => (l.statusPending, c.muted),
      ItemStatus.transferring => ('${(item.fraction * 100).toStringAsFixed(0)} %', Theme.of(context).colorScheme.primary),
    };
    return Padding(
      key: ValueKey('item-${item.index}'),
      padding: const EdgeInsets.fromLTRB(16, 8, 4, 8),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  item.path,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(fontWeight: FontWeight.w600),
                ),
                const SizedBox(height: 4),
                LinearProgressIndicator(value: item.fraction, minHeight: 4, borderRadius: BorderRadius.circular(2), color: color),
                const SizedBox(height: 2),
                Text('${formatBytes(context, item.size)} · $label', style: TextStyle(color: c.muted, fontSize: 12)),
              ],
            ),
          ),
          if (running && open && transfer.items.length > 1) ...[
            if (item.status == ItemStatus.paused)
              IconButton(
                key: ValueKey('resume-${item.index}'),
                tooltip: l.resumeFile,
                icon: const Icon(Icons.play_arrow_rounded),
                onPressed: () => unawaited(t.resumeFile(item.index)),
              )
            else
              IconButton(
                key: ValueKey('pause-${item.index}'),
                tooltip: l.pauseFile,
                icon: const Icon(Icons.pause_rounded),
                onPressed: () => unawaited(t.pauseFile(item.index)),
              ),
            IconButton(
              key: ValueKey('skip-${item.index}'),
              tooltip: l.skipFile,
              icon: const Icon(Icons.skip_next_rounded),
              onPressed: () => unawaited(t.skip(item.index)),
            ),
          ],
        ],
      ),
    );
  }
}

class _Footer extends StatelessWidget {
  const _Footer({required this.transfer, required this.canOpen});

  final ActiveTransfer transfer;
  final bool canOpen;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final state = AppScope.of(context).state;
    final t = state.transfers;
    final a = transfer;
    final done = a.stage == Stage.done;
    return SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
        child: Wrap(
          alignment: WrapAlignment.end,
          spacing: 8,
          runSpacing: 8,
          children: [
            if (!done) ...[
              TextButton(key: const Key('transfer-cancel'), onPressed: () => unawaited(t.cancel()), child: Text(l.cancel)),
              if (a.stage == Stage.running)
                FilledButton.tonalIcon(
                  key: const Key('transfer-pause'),
                  onPressed: () => unawaited(a.paused ? t.resume() : t.pause()),
                  icon: Icon(a.paused ? Icons.play_arrow_rounded : Icons.pause_rounded),
                  label: Text(a.paused ? l.resumeAll : l.pauseAll),
                ),
            ] else ...[
              if (canOpen && a.direction == Direction.received && a.outcome != Outcome.rejected)
                TextButton.icon(
                  key: const Key('open-folder'),
                  onPressed: () async => state.folderOpener.open(t.receiveDir ?? await state.folders.downloadDir()),
                  icon: const Icon(Icons.folder_open_rounded),
                  label: Text(l.openFolder),
                ),
              FilledButton(key: const Key('transfer-close'), onPressed: () => Navigator.of(context).maybePop(), child: Text(l.close)),
            ],
          ],
        ),
      ),
    );
  }
}
