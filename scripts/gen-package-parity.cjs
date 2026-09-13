/**
 * Gera os casos de paridade do pacote da sessão.
 *
 * Corre o **TypeScript verdadeiro** — compilado a partir de
 * `mobile/lib/session-package/from-engine.ts` — e não uma reimplementação. É o
 * que torna a comparação com o Go uma comparação, e não duas suposições.
 *
 *   cd mobile && npx tsc -p tsconfig.parity.json
 *   node api/scripts/gen-package-parity.cjs \
 *        api/internal/transport/http/view/testdata/ts-packages.json.gz
 */
const path = require('path');
const fs = require('fs');
const zlib = require('zlib');
const Module = require('module');

const BASE = path.resolve(__dirname, '../../.engine-build');

// `@/…` é o alias da app. Resolve-se aqui para o código compilado correr sem
// empacotador.
const original = Module._resolveFilename;
Module._resolveFilename = function (request, ...rest) {
  if (request.startsWith('@/')) {
    return original.call(this, path.join(BASE, request.slice(2)), ...rest);
  }
  return original.call(this, request, ...rest);
};

const { buildSession } = require(path.join(BASE, 'modules/workout-engine/index.js'));
const { packageFromEngine } = require(path.join(BASE, 'lib/session-package/from-engine.js'));

// Os oito rótulos de plano (incluindo um desconhecido, que cai em "full"), os
// três níveis, cinco combinações de equipamento, quatro orçamentos e três dias.
const labels = ['Full Body', 'Upper Body', 'Lower Body', 'Cardio', 'Mobilidade', 'Push', 'Pull', 'Desconhecido'];
const exps = ['beginner', 'intermediate', 'advanced'];
const equips = [[], ['dumbbells'], ['bands', 'mat'], ['barbell', 'machines'], ['bodyweight']];
const mins = [20, 30, 45, 60];
const days = ['2026-09-13', '2026-09-14', '2026-09-15'];

const cases = [];
for (const label of labels) {
  for (const exp of exps) {
    for (const eq of equips) {
      for (const min of mins) {
        for (const day of days) {
          const session = buildSession({
            planLabel: label, experience: exp, equipment: eq, minutes: min, dayISO: day,
          });
          cases.push({ label, exp, eq, min, day, pkg: packageFromEngine(session) });
        }
      }
    }
  }
}

const out = process.argv[2];
if (!out) {
  console.error('falta o caminho de saída');
  process.exit(1);
}
// Comprimido: 1440 pacotes em JSON são 36 MB, dez vezes o maior ficheiro de
// casos que já existe. A cobertura vale mais do que o formato, e o Go lê gzip
// sem dependência nenhuma.
fs.writeFileSync(out, zlib.gzipSync(JSON.stringify({ cases }), { level: 9 }));
const mb = (fs.statSync(out).size / 1048576).toFixed(1);
console.log(`${cases.length} pacotes, ${cases.reduce((n, c) => n + c.pkg.steps.length, 0)} passos → ${out} (${mb} MB)`);
