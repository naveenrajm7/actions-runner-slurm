'use client'
import {Button, Card, Grid, Hero} from '@primer/react-brand'
import Link from 'next/link'

type IndexProps = {
  title?: string
}

export default function Index({title = 'Actions Runner Slurm'}: IndexProps) {
  return (
    <>
      <div>
        <Hero align="center">
          <Hero.Eyebrow>GitHub Actions for Slurm clusters</Hero.Eyebrow>
          <Hero.Heading>{title}</Hero.Heading>
          <Hero.Description>
            Provision ephemeral GitHub Actions scale-set workers as containerized Slurm allocations.
          </Hero.Description>
          <Hero.ButtonGroup>
            <Button as={Link} href="/getting-started/">
              Get started
            </Button>
            <Button
              as="a"
              variant="secondary"
              href="https://github.com/naveenrajm7/actions-runner-slurm/releases"
            >
              Download a release
            </Button>
          </Hero.ButtonGroup>
        </Hero>
      </div>
      <section
        style={{
          ['--brand-Card-maxWidth' as string]: '100%',
          maxWidth: '800px',
          margin: '0 auto',
          ['--brand-Grid-spacing-row' as string]: 'var(--brand-Grid-spacing-column-gap)',
        }}
      >
        <Grid>
          <Grid.Column span={{xsmall: 12, medium: 4}}>
            <Link legacyBehavior passHref href="/getting-started/">
              <Card href="#" hasBorder style={{width: '100%'}}>
                <Card.Heading size="5">Get started</Card.Heading>
                <Card.Description>
                  Install the binary, connect GitHub and Slurm, and run a test workflow.
                </Card.Description>
              </Card>
            </Link>
          </Grid.Column>
          <Grid.Column span={{xsmall: 12, medium: 4}}>
            <Link legacyBehavior passHref href="/concepts/architecture/">
              <Card href="#" hasBorder style={{width: '100%'}}>
                <Card.Heading size="5">Understand the architecture</Card.Heading>
                <Card.Description>
                  See how GitHub scale sets, the service, Slurm, and disposable runners fit together.
                </Card.Description>
              </Card>
            </Link>
          </Grid.Column>
          <Grid.Column span={{xsmall: 12, medium: 4}}>
            <Link legacyBehavior passHref href="/reference/configuration/">
              <Card href="#" hasBorder style={{width: '100%'}}>
                <Card.Heading size="5">Configuration reference</Card.Heading>
                <Card.Description>
                  Review every service, GitHub, Slurm, resource, and execution-mode setting.
                </Card.Description>
              </Card>
            </Link>
          </Grid.Column>
        </Grid>
      </section>
    </>
  )
}
