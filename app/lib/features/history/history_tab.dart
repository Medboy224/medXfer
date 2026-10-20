/// The History tab: the last transfers, newest first (decision J8). A received one opens its
/// folder on the desktops.
library;

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';
import '../../transfer/history.dart';
import '../../widgets/format.dart';

class HistoryTab extends StatelessWidget {
  const HistoryTab({super.key});

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final t = state.transfers;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return ListenableBuilder(
      listenable: t,
      builder: (context, _) {
        final entries = t.entries;
        if (entries.isEmpty) {
          return Center(
            child: Padding(
              padding: const EdgeInsets.all(32),
              child: Text(l.historyEmpty, style: TextStyle(color: c.muted)),
            ),
          );
        }
        final dates = MaterialLocalizations.of(context);
        return ListView.separated(
          key: const Key('history'),
          itemCount: entries.length,
          separatorBuilder: (_, _) => Divider(height: 1, color: c.border),
          itemBuilder: (context, i) {
            final e = entries[i];
            final received = e.direction == Direction.received;
            final local = e.at.toLocal();
            final (outcome, color) = switch (e.outcome) {
              Outcome.completed => (l.statusCompleted, c.ok),
              Outcome.partial => (l.outcomePartial, c.muted),
              Outcome.canceled => (l.outcomeCanceled, c.muted),
              Outcome.rejected => (l.outcomeRejected, c.muted),
              Outcome.failed => (l.outcomeFailed, c.danger),
            };
            final canOpen = received && e.folder.isNotEmpty && state.folderOpener.supported && e.outcome != Outcome.rejected;
            return ListTile(
              key: ValueKey('history-$i'),
              leading: CircleAvatar(
                backgroundColor: c.chip,
                child: Icon(received ? Icons.south_west_rounded : Icons.north_east_rounded, color: Theme.of(context).colorScheme.primary),
              ),
              title: Text(
                e.name.isEmpty ? (received ? l.historyReceived(e.device) : l.historySent(e.device)) : e.name,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(fontWeight: FontWeight.w700),
              ),
              // The outcome in the subtitle: the longest one does not fit beside a phone's tile.
              subtitle: Text.rich(
                TextSpan(
                  children: [
                    TextSpan(text: '${received ? l.historyReceived(e.device) : l.historySent(e.device)}\n'),
                    TextSpan(text: '${l.filesCount(e.files)} · ${formatBytes(context, e.bytes)} · '),
                    TextSpan(text: outcome, style: TextStyle(color: color, fontWeight: FontWeight.w600)),
                    TextSpan(text: '\n${dates.formatShortDate(local)} ${dates.formatTimeOfDay(TimeOfDay.fromDateTime(local))}'),
                  ],
                ),
              ),
              isThreeLine: true,
              trailing: canOpen
                  ? IconButton(
                      key: ValueKey('history-open-$i'),
                      tooltip: l.openFolder,
                      icon: const Icon(Icons.folder_open_rounded),
                      onPressed: () => state.folderOpener.open(e.folder),
                    )
                  : null,
            );
          },
        );
      },
    );
  }
}
