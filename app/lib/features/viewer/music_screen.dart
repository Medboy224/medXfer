/// A song (WP-8.2, mockup 22): played where it is, the next one after it, « Sélectionner ». A
/// format the phone does not read is said, with another app to try.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../l10n/app_localizations.dart';
import '../../media/media_player.dart';
import '../../platform/library.dart';
import '../../theme.dart';
import 'open_media.dart';
import 'player_controls.dart';

class MusicScreen extends StatefulWidget {
  const MusicScreen({super.key, required this.items, required this.index});

  final List<MediaEntry> items;
  final int index;

  @override
  State<MusicScreen> createState() => _MusicScreenState();
}

class _MusicScreenState extends State<MusicScreen> {
  late MediaPlayer _player;
  late int _index = widget.index;
  bool _started = false;
  bool _advancing = false;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_started) return;
    _started = true;
    _player = AppScope.of(context, listen: false).state.players.create()..addListener(_ended);
    unawaited(_open(_index));
  }

  Future<void> _open(int i) async {
    if (i < 0 || i >= widget.items.length) return;
    setState(() => _index = i);
    await _player.open(widget.items[i]);
    _advancing = false;
    if (mounted && _player.error == null) await _player.play();
  }

  /// A song played to its end: the next one, as a player of music does.
  void _ended() {
    if (!_player.finished || _advancing || _index >= widget.items.length - 1) return;
    _advancing = true;
    unawaited(_open(_index + 1));
  }

  @override
  void dispose() {
    _player
      ..removeListener(_ended)
      ..dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final m = widget.items[_index];
    return Theme(
      data: ThemeData.dark(useMaterial3: true).copyWith(colorScheme: ColorScheme.dark(primary: c.accent)),
      child: Scaffold(
        backgroundColor: c.background,
        body: SafeArea(
          child: ListenableBuilder(
            listenable: _player,
            builder: (context, _) {
              final p = _player;
              final failed = p.error != null;
              return ListView(
                padding: const EdgeInsets.only(bottom: 16),
                children: [
                  PlayerHeader(entry: m, line: l.positionOf(_index + 1, widget.items.length), close: Icons.keyboard_arrow_down_rounded),
                  Center(
                    child: Container(
                      width: 260,
                      height: 260,
                      margin: const EdgeInsets.symmetric(vertical: 16),
                      decoration: BoxDecoration(color: c.surface, borderRadius: BorderRadius.circular(24), border: Border.all(color: c.border)),
                      child: Icon(Icons.music_note_rounded, size: 96, color: c.accent),
                    ),
                  ),
                  Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 24),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(m.name, style: titleStyle(20, weight: FontWeight.w900)),
                        const SizedBox(height: 4),
                        Text(mediaDetails(context, m) + (m.album.isEmpty ? '' : ' · ${m.album}'), style: TextStyle(color: c.muted, fontSize: 14)),
                      ],
                    ),
                  ),
                  const SizedBox(height: 8),
                  if (failed)
                    CannotPlay(
                      accent: c.accent,
                      unsupported: !state.players.supported,
                      onOpenWith: state.openWith.supported ? () => unawaited(state.openWith.open(m)) : null,
                    )
                  else
                    PlayerControls(
                      player: p,
                      accent: c.accent,
                      onPrevious: _index > 0 ? () => unawaited(_open(_index - 1)) : null,
                      onNext: _index < widget.items.length - 1 ? () => unawaited(_open(_index + 1)) : null,
                    ),
                  Padding(padding: const EdgeInsets.fromLTRB(14, 8, 14, 0), child: MediaSelectButton(entry: m, accent: c.accent)),
                ],
              );
            },
          ),
        ),
      ),
    );
  }
}
