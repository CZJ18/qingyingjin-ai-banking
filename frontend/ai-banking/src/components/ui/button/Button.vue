<script setup lang="ts">
import { Primitive, type PrimitiveProps } from 'reka-ui'
import { cva } from 'class-variance-authority'
import { cn } from '@/lib/utils'
const props = withDefaults(
  defineProps<
    PrimitiveProps & {
      variant?: 'default' | 'outline' | 'ghost' | 'danger'
      class?: string
      disabled?: boolean
      type?: 'button' | 'submit' | 'reset'
    }
  >(),
  { as: 'button', variant: 'default', type: 'button' },
)
const variants = cva('btn', {
  variants: {
    variant: {
      default: 'btn-primary',
      outline: 'btn-outline',
      ghost: 'btn-ghost',
      danger: 'btn-danger',
    },
  },
})
</script>
<template>
  <Primitive
    :as="props.as"
    :as-child="props.asChild"
    :class="cn(variants({ variant }), props.class)"
    :disabled="disabled"
    :type="props.as === 'button' ? props.type : undefined"
    ><slot
  /></Primitive>
</template>
