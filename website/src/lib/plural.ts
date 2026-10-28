// count writes a number with its noun in the form that agrees with it: "1 turn",
// "2 turns". Pass the plural when the noun does not simply add an s.
export function count(n: number, singular: string, plural = `${singular}s`): string {
  return `${n} ${n === 1 ? singular : plural}`;
}
