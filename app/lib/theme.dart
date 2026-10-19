/// medXfer's own look (decision J15 of the J6b plan): light, dark and black themes, four
/// accents, the Figtree type. The tokens are those of the validated mockups.
library;

import 'package:flutter/material.dart';

/// The themes the user picks from (UI-4); [system] follows the device.
enum ThemeChoice { system, light, dark, black }

/// The accents offered: blue by default, then green, orange, violet. Each keeps white text on
/// it readable (4.5:1 at least).
const accents = <Color>[Color(0xFF2563EB), Color(0xFF0B7A55), Color(0xFFC2410C), Color(0xFF7C3AED)];

/// The colors of one theme, beside Material's scheme: what the screens draw with.
@immutable
class MedxferColors extends ThemeExtension<MedxferColors> {
  const MedxferColors({
    required this.background,
    required this.surface,
    required this.section,
    required this.text,
    required this.muted,
    required this.border,
    required this.chip,
    required this.ok,
    required this.danger,
  });

  static const light = MedxferColors(
    background: Color(0xFFF4F5F7),
    surface: Color(0xFFFFFFFF),
    section: Color(0xFFE9ECF0),
    text: Color(0xFF14161A),
    muted: Color(0xFF5B6270),
    border: Color(0xFFE3E6EB),
    chip: Color(0xFFE6E9EE),
    ok: Color(0xFF0B7A55),
    danger: Color(0xFFB42318),
  );

  static const dark = MedxferColors(
    background: Color(0xFF0F1115),
    surface: Color(0xFF181B21),
    section: Color(0xFF1E2128),
    text: Color(0xFFF1F3F6),
    muted: Color(0xFFA3AAB6),
    border: Color(0xFF2A2F38),
    chip: Color(0xFF222730),
    ok: Color(0xFF4ADE80),
    danger: Color(0xFFF97066),
  );

  /// Black, for OLED screens.
  static const black = MedxferColors(
    background: Color(0xFF000000),
    surface: Color(0xFF0B0C0E),
    section: Color(0xFF121418),
    text: Color(0xFFF1F3F6),
    muted: Color(0xFF9AA1AD),
    border: Color(0xFF1F232A),
    chip: Color(0xFF16191E),
    ok: Color(0xFF4ADE80),
    danger: Color(0xFFF97066),
  );

  final Color background;
  final Color surface;
  final Color section;
  final Color text;
  final Color muted;
  final Color border;
  final Color chip;
  final Color ok;
  final Color danger;

  @override
  MedxferColors copyWith() => this;

  @override
  MedxferColors lerp(MedxferColors? other, double t) {
    if (other == null) return this;
    Color c(Color a, Color b) => Color.lerp(a, b, t)!;
    return MedxferColors(
      background: c(background, other.background),
      surface: c(surface, other.surface),
      section: c(section, other.section),
      text: c(text, other.text),
      muted: c(muted, other.muted),
      border: c(border, other.border),
      chip: c(chip, other.chip),
      ok: c(ok, other.ok),
      danger: c(danger, other.danger),
    );
  }
}

/// The theme for colors and accent.
ThemeData buildTheme(MedxferColors colors, Color accent, Brightness brightness) {
  final scheme = ColorScheme.fromSeed(seedColor: accent, brightness: brightness).copyWith(
    primary: accent,
    onPrimary: Colors.white,
    surface: colors.surface,
    onSurface: colors.text,
    outline: colors.border,
    error: colors.danger,
  );
  return ThemeData(
    useMaterial3: true,
    brightness: brightness,
    colorScheme: scheme,
    fontFamily: 'Figtree',
    scaffoldBackgroundColor: colors.background,
    dividerColor: colors.border,
    extensions: [colors],
    tabBarTheme: TabBarThemeData(
      labelColor: accent,
      unselectedLabelColor: colors.muted,
      indicatorColor: accent,
      labelStyle: const TextStyle(fontFamily: 'Figtree', fontWeight: FontWeight.w700, fontSize: 15),
      unselectedLabelStyle: const TextStyle(fontFamily: 'Figtree', fontWeight: FontWeight.w700, fontSize: 15),
      tabAlignment: TabAlignment.start,
      dividerColor: Colors.transparent,
    ),
  );
}

extension MedxferTheme on BuildContext {
  MedxferColors get colors => Theme.of(this).extension<MedxferColors>() ?? MedxferColors.light;
}
