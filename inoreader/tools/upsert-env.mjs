// Upsert a single `export VAR='value'` line in an env file without disturbing
// the rest. Value is read from CAP_VALUE to avoid shell-quoting hazards.
//   CAP_VALUE='...' node upsert-env.mjs <envfile> <VARNAME>
import fs from 'node:fs';

const [envfile, varname] = process.argv.slice(2);
if (!envfile || !varname) {
  console.error('usage: CAP_VALUE=... node upsert-env.mjs <envfile> <VARNAME>');
  process.exit(2);
}
const value = process.env.CAP_VALUE ?? '';
if (!value) {
  console.error('CAP_VALUE is empty; refusing to write a blank credential');
  process.exit(1);
}

const line = `export ${varname}='${value.replace(/'/g, "'\\''")}'`;
let lines = [];
try {
  lines = fs.readFileSync(envfile, 'utf8').split('\n');
} catch {
  // new file
}
const re = new RegExp(`^\\s*(export\\s+)?${varname}=`);
let found = false;
lines = lines.map((l) => {
  if (re.test(l)) {
    found = true;
    return line;
  }
  return l;
});
if (!found) {
  if (lines.length && lines[lines.length - 1] === '') lines.pop();
  lines.push(line, '');
}
fs.writeFileSync(envfile, lines.join('\n'));
console.log(`wrote ${varname} (${value.length} chars) to ${envfile}`);
