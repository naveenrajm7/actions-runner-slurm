import {readFile, writeFile} from 'node:fs/promises'
import {resolve} from 'node:path'

const headerPath = resolve('node_modules/@primer/doctocat-nextjs/components/layout/header/Header.tsx')
const original = await readFile(headerPath, 'utf8')

const replacements = [
  ["import React, {useEffect, useRef, useState} from 'react'", "import React, {useEffect, useRef, useState} from 'react'\nimport Link from 'next/link'"],
  ['<a href="/" className={styles.Header__siteTitle}>', '<Link href="/" className={styles.Header__siteTitle}>'],
  ['</a>\n        <Text as="span" className={styles.Header__separator}', '</Link>\n        <Text as="span" className={styles.Header__separator}'],
  ['<a\n                className={styles.Header__link}', '<Link\n                className={styles.Header__link}'],
  ['</a>\n            </li>', '</Link>\n            </li>'],
]

let patched = original
for (const [expected, replacement] of replacements) {
  if (patched.includes(replacement)) continue
  if (!patched.includes(expected)) {
    throw new Error(`Doctocat header patch no longer applies: missing ${JSON.stringify(expected)}`)
  }
  patched = patched.replace(expected, replacement)
}

await writeFile(headerPath, patched)

const footerPath = resolve('node_modules/@primer/doctocat-nextjs/components/layout/footer/Footer.tsx')
const footer = await readFile(footerPath, 'utf8')
const footerCopyright = '&copy; {new Date().getFullYear()} GitHub, Inc. All rights reserved.'
const footerAttribution = 'Actions Runner Slurm is an independent open-source project and is not affiliated with GitHub.'
if (!footer.includes(footerCopyright) && !footer.includes(footerAttribution)) {
  throw new Error('Doctocat footer patch no longer applies: expected copyright was not found')
}

await writeFile(
  footerPath,
  footer.replace(footerCopyright, footerAttribution),
)
