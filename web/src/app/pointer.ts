/** A touch device whose primary pointer is coarse (phones, iPad): focusing a box there raises the keyboard. */
export const coarsePointer = () => matchMedia('(pointer: coarse)').matches;
