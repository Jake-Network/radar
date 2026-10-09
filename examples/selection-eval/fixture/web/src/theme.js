export const palette = { primary: "#1d4ed8", surface: "#ffffff" };

export function contrastText(background) {
  return background === palette.surface ? "#111827" : "#ffffff";
}
