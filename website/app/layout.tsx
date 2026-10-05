import type {Metadata} from 'next'
import type {ReactNode} from 'react'
import Theme, {getPageMap} from '@primer/doctocat-nextjs'
import '@primer/doctocat-nextjs/css/global.css'

const siteTitle = 'Actions Runner Slurm'

export const metadata: Metadata = {
  title: {
    default: siteTitle,
    template: `%s - ${siteTitle}`,
  },
  description: 'Run ephemeral GitHub Actions scale-set workers as Slurm allocations.',
}

type ThemeProps = Parameters<typeof Theme>[0]

const headerLinks: ThemeProps['headerLinks'] = [
  {
    href: '/',
    title: 'Documentation',
    isActive: true,
  },
  {
    href: 'https://github.com/naveenrajm7/actions-runner-slurm',
    title: 'GitHub',
    isExternal: true,
  },
  {
    href: 'https://github.com/naveenrajm7/actions-runner-slurm/releases',
    title: 'Releases',
    isExternal: true,
  },
]

export default async function RootLayout({children}: Readonly<{children: ReactNode}>) {
  const pageMap = await getPageMap()

  return (
    <html lang="en" dir="ltr" className="js-focus-visible" data-js-focus-visible="">
      <body>
        <Theme pageMap={pageMap} headerLinks={headerLinks}>
          {children}
        </Theme>
      </body>
    </html>
  )
}
