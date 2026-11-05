/* Writes past the end of a heap buffer with strcpy. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

char *make_label(const char *name)
{
	char *label = malloc(8);
	strcpy(label, name);
	return label;
}

void print_label(const char *name)
{
	char *label = make_label(name);
	puts(label);
	free(label);
}

int main(int argc, char **argv)
{
	print_label(argc > 1 ? argv[1] : "a label that is too long");
	return 0;
}
