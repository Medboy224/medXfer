/// medXfer's own look, « Version 4 » of the mockups (plan J8, WP-8.1): dark by default, one
/// accent, big figures, the Archivo type. Light, dark and black themes, four accents.
library;

import 'package:flutter/material.dart';

/// The themes the user picks from (UI-4); [system] follows the device.
enum ThemeChoice { system, light, dark, black }

/// One accent: [bright] for the dark themes (and the text on the light theme's dark buttons),
/// [deep] for the icons and links of the light theme. Both read at 4.5:1 at least where used.
@immutable
class Accent {
  const Accent(this.bright, this.deep);

  final Color bright;
  final Color deep;

  @override
  bool operator ==(Object other) => other is Accent && other.bright == bright && other.deep == deep;

  @override
  int get hashCode => Object.hash(bright, deep);
}

/// The accents offered: volt by default, then blue, orange, violet.
const accents = <Accent>[
  Accent(Color(0xFFC5F03A), Color(0xFF4A6B00)),
  Accent(Color(0xFF7CC4FF), Color(0xFF0B5CAD)),
  Accent(Color(0xFFFFA95A), Color(0xFFA34700)),
  Accent(Color(0xFFC4AEFF), Color(0xFF5B34C9)),
];

/// The colors of a theme that do not depend on the accent.
@immutable
class Palette {
  const Palette({
    required this.brightness,
    required this.background,
    required this.surface,
    required this.raised,
    required this.line,
    required this.text,
    required this.muted,
    required this.danger,
    required this.warn,
    required this.shadow,
  });

  final Brightness brightness;
  final Color background;
  final Color surface;
  final Color raised;
  final Color line;
  final Color text;
  final Color muted;
  final Color danger;
  final Color warn;
  final Color shadow;

  static const light = Palette(
    brightness: Brightness.light,
    background: Color(0xFFECEEE7),
    surface: Color(0xFFFFFFFF),
    raised: Color(0xFFE2E5DC),
    line: Color(0xFFC9CDC2),
    text: Color(0xFF111312),
    muted: Color(0xFF4D544D),
    danger: Color(0xFFB3261E),
    warn: Color(0xFF8A5300),
    shadow: Color(0x33111312),
  );

  static const dark = Palette(
    brightness: Brightness.dark,
    background: Color(0xFF0D0F0E),
    surface: Color(0xFF181B19),
    raised: Color(0xFF242826),
    line: Color(0xFF343936),
    text: Color(0xFFF3F5F0),
    muted: Color(0xFFA7AEA6),
    danger: Color(0xFFFF8A7A),
    warn: Color(0xFFFFC24B),
    shadow: Color(0x99000000),
  );

  /// Black, for OLED screens.
  static const black = Palette(
    brightness: Brightness.dark,
    background: Color(0xFF000000),
    surface: Color(0xFF111312),
    raised: Color(0xFF1C1F1D),
    line: Color(0xFF2A2E2B),
    text: Color(0xFFF3F5F0),
    muted: Color(0xFFA0A79F),
    danger: Color(0xFFFF8A7A),
    warn: Color(0xFFFFC24B),
    shadow: Color(0x99000000),
  );
}

/// The colors the screens draw with, for one theme and one accent.
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
    required this.warn,
    required this.accent,
    required this.strong,
    required this.onStrong,
    required this.soft,
    required this.shadow,
  });

  factory MedxferColors.of(Palette p, Accent a) {
    final light = p.brightness == Brightness.light;
    return MedxferColors(
      background: p.background,
      surface: p.surface,
      section: p.raised,
      text: p.text,
      muted: p.muted,
      border: p.line,
      chip: p.raised,
      ok: light ? a.deep : a.bright,
      danger: p.danger,
      warn: p.warn,
      accent: light ? a.deep : a.bright,
      strong: light ? p.text : a.bright,
      onStrong: light ? a.bright : p.background,
      soft: light ? a.bright.withAlpha(0x66) : a.bright.withAlpha(0x26),
      shadow: p.shadow,
    );
  }

  static final light = MedxferColors.of(Palette.light, accents.first);
  static final dark = MedxferColors.of(Palette.dark, accents.first);
  static final black = MedxferColors.of(Palette.black, accents.first);

  final Color background;
  final Color surface;

  /// A raised surface: menus, chips, the floating bars.
  final Color section;
  final Color text;
  final Color muted;
  final Color border;
  final Color chip;

  /// Success and « connected »: the accent, as everywhere in V4.
  final Color ok;
  final Color danger;

  /// Warnings (the firewall, the certificate): never the accent.
  final Color warn;

  /// Icons, links, figures.
  final Color accent;

  /// The main action of a screen, and the text on it.
  final Color strong;
  final Color onStrong;

  /// A tinted background: a selected row, a banner.
  final Color soft;
  final Color shadow;

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
      warn: c(warn, other.warn),
      accent: c(accent, other.accent),
      strong: c(strong, other.strong),
      onStrong: c(onStrong, other.onStrong),
      soft: c(soft, other.soft),
      shadow: c(shadow, other.shadow),
    );
  }
}

/// The type: Archivo (SIL Open Font License 1.1, assets/fonts/OFL.txt), a variable font whose
/// weight follows [FontWeight]; [wide] stretches it (titles, figures).
const fontFamily = 'Archivo';

/// The width axis at 125 %: the titles and the big figures of V4.
const wide = <FontVariation>[FontVariation('wdth', 125)];

/// A wide, heavy title.
TextStyle titleStyle(double size, {FontWeight weight = FontWeight.w800, Color? color}) =>
    TextStyle(fontFamily: fontFamily, fontSize: size, fontWeight: weight, fontVariations: wide, letterSpacing: -0.2, color: color);

/// Figures that line up (sizes, speeds, codes).
const tabular = <FontFeature>[FontFeature.tabularFigures()];

/// The radii of V4: controls 14, cards 18, sheets 24.
const controlRadius = 14.0;
const cardRadius = 18.0;

/// The theme for a palette and an accent.
ThemeData buildTheme(Palette palette, Accent accent) {
  final colors = MedxferColors.of(palette, accent);
  final brightness = palette.brightness;
  final shape = RoundedRectangleBorder(borderRadius: BorderRadius.circular(controlRadius));
  const label = TextStyle(fontFamily: fontFamily, fontWeight: FontWeight.w800, fontSize: 14);
  final scheme = ColorScheme.fromSeed(seedColor: accent.bright, brightness: brightness).copyWith(
    primary: colors.accent,
    onPrimary: brightness == Brightness.light ? Colors.white : palette.background,
    surface: colors.surface,
    onSurface: colors.text,
    onSurfaceVariant: colors.muted,
    surfaceContainerHighest: colors.section,
    outline: colors.border,
    outlineVariant: colors.border,
    error: colors.danger,
  );
  return ThemeData(
    useMaterial3: true,
    brightness: brightness,
    colorScheme: scheme,
    fontFamily: fontFamily,
    scaffoldBackgroundColor: colors.background,
    canvasColor: colors.background,
    dividerColor: colors.border,
    extensions: [colors],
    appBarTheme: AppBarTheme(
      backgroundColor: colors.background,
      surfaceTintColor: Colors.transparent,
      foregroundColor: colors.text,
      titleTextStyle: titleStyle(18, color: colors.text),
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        backgroundColor: colors.strong,
        foregroundColor: colors.onStrong,
        disabledBackgroundColor: colors.section,
        disabledForegroundColor: colors.muted,
        minimumSize: const Size(44, 44),
        shape: shape,
        textStyle: label,
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        foregroundColor: colors.text,
        side: BorderSide(color: colors.border, width: 2),
        minimumSize: const Size(44, 44),
        shape: shape,
        textStyle: label.copyWith(fontWeight: FontWeight.w700),
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(
        foregroundColor: colors.accent,
        minimumSize: const Size(44, 44),
        shape: shape,
        textStyle: label.copyWith(fontWeight: FontWeight.w700, fontSize: 14),
      ),
    ),
    iconButtonTheme: IconButtonThemeData(style: IconButton.styleFrom(minimumSize: const Size(44, 44), shape: shape)),
    cardTheme: CardThemeData(
      color: colors.surface,
      surfaceTintColor: Colors.transparent,
      elevation: 0,
      margin: EdgeInsets.zero,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(cardRadius)),
    ),
    // Square boxes: several choices; round: one choice (V4).
    checkboxTheme: CheckboxThemeData(
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
      side: BorderSide(color: colors.muted, width: 2),
      fillColor: WidgetStateProperty.resolveWith((s) => s.contains(WidgetState.selected) ? colors.strong : Colors.transparent),
      checkColor: WidgetStatePropertyAll(colors.onStrong),
    ),
    radioTheme: RadioThemeData(
      fillColor: WidgetStateProperty.resolveWith((s) => s.contains(WidgetState.selected) ? colors.accent : colors.muted),
    ),
    switchTheme: SwitchThemeData(
      thumbColor: WidgetStateProperty.resolveWith((s) => s.contains(WidgetState.selected) ? colors.onStrong : colors.muted),
      trackColor: WidgetStateProperty.resolveWith((s) => s.contains(WidgetState.selected) ? colors.strong : Colors.transparent),
      trackOutlineColor: WidgetStateProperty.resolveWith((s) => s.contains(WidgetState.selected) ? colors.strong : colors.muted),
    ),
    segmentedButtonTheme: SegmentedButtonThemeData(
      style: ButtonStyle(
        minimumSize: const WidgetStatePropertyAll(Size(0, 44)),
        backgroundColor: WidgetStateProperty.resolveWith((s) => s.contains(WidgetState.selected) ? colors.strong : colors.section),
        foregroundColor: WidgetStateProperty.resolveWith((s) => s.contains(WidgetState.selected) ? colors.onStrong : colors.text),
        side: WidgetStatePropertyAll(BorderSide(color: colors.section, width: 2)),
        shape: WidgetStatePropertyAll(RoundedRectangleBorder(borderRadius: BorderRadius.circular(controlRadius))),
        textStyle: const WidgetStatePropertyAll(TextStyle(fontFamily: fontFamily, fontWeight: FontWeight.w700, fontSize: 14)),
      ),
    ),
    progressIndicatorTheme: ProgressIndicatorThemeData(color: colors.accent, linearTrackColor: colors.section),
    popupMenuTheme: PopupMenuThemeData(
      color: colors.section,
      surfaceTintColor: Colors.transparent,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(cardRadius)),
      textStyle: TextStyle(fontFamily: fontFamily, fontSize: 15, fontWeight: FontWeight.w600, color: colors.text),
    ),
    dialogTheme: DialogThemeData(
      backgroundColor: colors.section,
      surfaceTintColor: Colors.transparent,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(24)),
      titleTextStyle: titleStyle(20, color: colors.text),
    ),
    bottomSheetTheme: BottomSheetThemeData(
      backgroundColor: colors.section,
      surfaceTintColor: Colors.transparent,
      shape: const RoundedRectangleBorder(borderRadius: BorderRadius.vertical(top: Radius.circular(24))),
    ),
    snackBarTheme: SnackBarThemeData(
      backgroundColor: colors.text,
      contentTextStyle: TextStyle(fontFamily: fontFamily, color: colors.background, fontWeight: FontWeight.w600),
      behavior: SnackBarBehavior.floating,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(controlRadius)),
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: colors.surface,
      border: OutlineInputBorder(borderRadius: BorderRadius.circular(controlRadius), borderSide: BorderSide(color: colors.border, width: 2)),
      enabledBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(controlRadius), borderSide: BorderSide(color: colors.border, width: 2)),
      focusedBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(controlRadius), borderSide: BorderSide(color: colors.accent, width: 2)),
    ),
    listTileTheme: ListTileThemeData(iconColor: colors.accent, minVerticalPadding: 10, shape: shape),
    tabBarTheme: TabBarThemeData(
      labelColor: colors.onStrong,
      unselectedLabelColor: colors.muted,
      indicator: BoxDecoration(color: colors.strong, borderRadius: BorderRadius.circular(controlRadius)),
      indicatorSize: TabBarIndicatorSize.tab,
      labelStyle: const TextStyle(fontFamily: fontFamily, fontWeight: FontWeight.w700, fontSize: 13),
      unselectedLabelStyle: const TextStyle(fontFamily: fontFamily, fontWeight: FontWeight.w700, fontSize: 13),
      tabAlignment: TabAlignment.start,
      dividerColor: Colors.transparent,
      labelPadding: const EdgeInsets.symmetric(horizontal: 14),
      splashBorderRadius: BorderRadius.circular(controlRadius),
    ),
  );
}

extension MedxferTheme on BuildContext {
  MedxferColors get colors => Theme.of(this).extension<MedxferColors>() ?? MedxferColors.light;
}
