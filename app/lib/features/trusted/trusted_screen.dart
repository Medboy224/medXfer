/// The trusted devices (PAIR-08): rename one, forget one. Adding one happens only by mutual
/// trust, after a pairing.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../connection/connection.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';

class TrustedScreen extends StatefulWidget {
  const TrustedScreen({super.key});

  @override
  State<TrustedScreen> createState() => _TrustedScreenState();
}

class _TrustedScreenState extends State<TrustedScreen> {
  late final Connection _c = AppScope.of(context, listen: false).state.connection;

  @override
  void initState() {
    super.initState();
    unawaited(_c.loadTrusted());
  }

  Future<void> _rename(TrustedDevice d) async {
    final name = await showDialog<String>(context: context, builder: (_) => _RenameDialog(initial: d.alias));
    if (name != null && name.trim().isNotEmpty && name.trim() != d.alias) await _c.rename(d, name);
  }

  Future<void> _forget(TrustedDevice d) async {
    final l = AppLocalizations.of(context);
    final sure = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(l.forgetTitle(d.alias)),
        content: Text(l.forgetBody),
        actions: [
          TextButton(onPressed: () => Navigator.of(context).pop(false), child: Text(l.cancel)),
          FilledButton(
            key: const Key('forget-ok'),
            style: FilledButton.styleFrom(backgroundColor: context.colors.danger),
            onPressed: () => Navigator.of(context).pop(true),
            child: Text(l.forget),
          ),
        ],
      ),
    );
    if (sure == true) await _c.forget(d);
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return Scaffold(
      appBar: AppBar(title: Text(l.trustedDevices)),
      body: ListenableBuilder(
        listenable: _c,
        builder: (context, _) {
          final devices = _c.trusted;
          if (devices.isEmpty) {
            return Center(
              child: Padding(padding: const EdgeInsets.all(32), child: Text(l.trustedEmpty, style: TextStyle(color: c.muted))),
            );
          }
          return ListView.separated(
            itemCount: devices.length,
            separatorBuilder: (_, _) => Divider(height: 1, color: c.border),
            itemBuilder: (context, i) {
              final d = devices[i];
              return ListTile(
                key: ValueKey('trusted-${d.fingerprint}'),
                leading: CircleAvatar(backgroundColor: c.chip, child: Icon(Icons.verified_user_rounded, color: Theme.of(context).colorScheme.primary)),
                title: Text(d.alias.isEmpty ? d.short : d.alias, style: const TextStyle(fontWeight: FontWeight.w700)),
                subtitle: Text([l.keyLabel(d.short), if (d.lastAddress.isNotEmpty) d.lastAddress].join(' · ')),
                trailing: PopupMenuButton<String>(
                  key: ValueKey('trusted-menu-${d.fingerprint}'),
                  tooltip: l.menu,
                  onSelected: (v) => unawaited(v == 'rename' ? _rename(d) : _forget(d)),
                  itemBuilder: (_) => [
                    PopupMenuItem(value: 'rename', child: Text(l.rename)),
                    PopupMenuItem(value: 'forget', child: Text(l.forget)),
                  ],
                ),
              );
            },
          );
        },
      ),
    );
  }
}

/// The new name of a trusted device. Its own state: the field lives as long as the window,
/// closing animation included.
class _RenameDialog extends StatefulWidget {
  const _RenameDialog({required this.initial});

  final String initial;

  @override
  State<_RenameDialog> createState() => _RenameDialogState();
}

class _RenameDialogState extends State<_RenameDialog> {
  late final _field = TextEditingController(text: widget.initial);

  @override
  void dispose() {
    _field.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    return AlertDialog(
      title: Text(l.rename),
      content: TextField(
        key: const Key('rename-field'),
        controller: _field,
        autofocus: true,
        maxLength: 64,
        decoration: InputDecoration(labelText: l.nameField),
        onSubmitted: (v) => Navigator.of(context).pop(v),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.of(context).pop(), child: Text(l.cancel)),
        FilledButton(key: const Key('rename-ok'), onPressed: () => Navigator.of(context).pop(_field.text), child: Text(l.save)),
      ],
    );
  }
}
