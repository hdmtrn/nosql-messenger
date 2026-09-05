<script setup>
import TopBar from '../components/TopBar.vue'
import ChannelRow from '../components/ChannelRow.vue'
import PaneHeader from '../components/PaneHeader.vue'
import MessageBubble from '../components/MessageBubble.vue'
import MessageComposer from '../components/MessageComposer.vue'
import SgButton from '../components/SgButton.vue'

const channels = [
  { name: 'general', active: true },
  { name: 'project-x', unread: 3 },
  { name: 'random' },
  { name: 'design-review', unread: 12 },
  { name: 'deploys' },
  { name: 'infra-notes' },
]

const messages = [
  { author: 'theo', initials: 'TH', time: '08:12', status: 'delivered', text: "Staging is back up. Redeploy whenever you're ready." },
  { author: 'rue', initials: 'RU', time: '08:14', status: 'delivered', text: "Then I'll cut the release notes after lunch." },
  { author: 'mara', initials: 'MA', time: '08:15', status: 'delivered', own: true, text: 'Redeploying now.' },
  { author: 'mara', initials: 'MA', status: 'sending', own: true, text: 'Notes look fine to me.' },
  { author: 'mara', initials: 'MA', status: 'failed', own: true, text: 'Pushing the tag in a minute.' },
]
</script>

<template>
  <div style="height:100vh;display:flex;flex-direction:column;background:var(--paper);
              padding:0 16px 16px;box-sizing:border-box">
    <TopBar product="Messenger" user="mara" initials="MA" />

    <div style="flex:1;min-height:0;display:flex;gap:16px">
      <nav style="flex:0 0 286px;background:var(--surface-accent);border-radius:var(--radius-panel);
                  padding:20px 16px;display:flex;flex-direction:column;gap:2px">
        <span class="sg-mono" style="color:var(--text-on-blue-muted);padding:0 14px 12px">Channels</span>
        <div style="display:flex;gap:8px;padding:0 8px 16px">
          <SgButton variant="outline" size="sm" on-blue>+ New</SgButton>
          <SgButton variant="outline" size="sm" on-blue>Join by code</SgButton>
        </div>
        <ChannelRow
          v-for="c in channels"
          :key="c.name"
          :name="c.name"
          :active="!!c.active"
          :unread="c.unread || 0"
        />
      </nav>

      <section style="flex:1;min-width:0;background:var(--surface-panel);
                      border-radius:var(--radius-panel);display:flex;flex-direction:column;overflow:hidden">
        <PaneHeader title="general" code="7K2-QD9-51" />
        <div style="flex:1;overflow:hidden;padding:22px 28px;display:flex;
                    flex-direction:column;justify-content:flex-end;gap:18px">
          <MessageBubble
            v-for="(m, i) in messages"
            :key="i"
            :own="!!m.own"
            :status="m.status"
            :author="m.author"
            :initials="m.initials"
            :time="m.time"
          >{{ m.text }}</MessageBubble>
        </div>
        <MessageComposer placeholder="Message #general…" />
      </section>
    </div>
  </div>
</template>
